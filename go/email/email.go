package email

import (
	"context"
	"net/mail"
	"net/smtp"

	"goweb/go/storage/config"

	"github.com/Data-Corruption/stdx/xhttp"
)

const (
	smtpServer = "smtp.gmail.com"
	smtpPort   = "587"
)

var ErrNotConfigured = &xhttp.Err{Code: 500, Msg: "email service not configured", Err: nil}

// IsConfigured checks if the email service is configured correctly.
// Returns nil if configured, ErrNotConfigured if not configured, or an error
// if there was an issue retrieving the configuration.
func IsConfigured(ctx context.Context) error {
	enabled, sender, pass, err := getConfig(ctx)
	if err != nil {
		return err
	}
	if !enabled || sender == "" || pass == "" {
		return ErrNotConfigured
	}
	return nil
}

// IsAddressValid checks if the given email is valid.
// It does not check if the email is already taken.
func IsAddressValid(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// SendEmail sends an email to the specified email address.
func SendEmail(ctx context.Context, to, subject, body string) error {
	enabled, sender, pass, err := getConfig(ctx)
	if err != nil {
		return err
	}

	// if not configured, return an error
	if !enabled || sender == "" || pass == "" {
		return ErrNotConfigured
	}

	// setup message
	message := []byte("To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")

	// SMTP server configuration.
	auth := smtp.PlainAuth("", sender, pass, smtpServer)

	// TLS connection to send the email
	addr := smtpServer + ":" + smtpPort
	return smtp.SendMail(addr, auth, sender, []string{to}, message)
}

func getConfig(ctx context.Context) (bool, string, string, error) {
	var err error
	var enabled bool
	var sender, pass string

	if enabled, err = config.Get[bool](ctx, "enableEmail"); err != nil {
		return false, "", "", err
	}
	if sender, err = config.Get[string](ctx, "emailSender"); err != nil {
		return false, "", "", err
	}
	if pass, err = config.Get[string](ctx, "emailPassword"); err != nil {
		return false, "", "", err
	}
	return enabled, sender, pass, nil
}
