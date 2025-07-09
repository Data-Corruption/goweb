// Package storagepath is the path to the directory where all data related to the tool is stored.
package storagepath

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, ctxKey{}, path)
}

func FromContext(ctx context.Context) string {
	if path, ok := ctx.Value(ctxKey{}).(string); ok {
		return path
	}
	return ""
}

func Init(ctx context.Context, alt, name string) (context.Context, error) {
	if path := FromContext(ctx); path != "" {
		return ctx, fmt.Errorf("storage path already initialized in context")
	}
	path := ""
	if alt != "" {
		path = alt
		if path == "" || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("storage path must be an absolute path")
		}
	} else {
		home := os.Getenv("HOME")
		if home == "" || !filepath.IsAbs(home) {
			return nil, fmt.Errorf("HOME environment variable is not defined or not an absolute path")
		}
		path = filepath.Join(home, "."+name)
	}
	// Ensure storage path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create storage directory: %v", err)
		}
	}
	return IntoContext(ctx, path), nil
}
