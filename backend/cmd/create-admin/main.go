// create-admin bootstraps the first account while the application is stopped.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/config"
	"github.com/pinkhit/vault/backend/internal/database"
	"github.com/pinkhit/vault/backend/internal/storage"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	syscall.Umask(0077)
	username := flag.String("username", "", "initial administrator username (required)")
	stdin := flag.Bool("password-stdin", false, "read password from stdin for automation; never pass passwords as arguments")
	flag.Parse()
	if *username == "" {
		return fmt.Errorf("provide -username; passwords are prompted securely")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	store, err := storage.Open(c.DataDir)
	if err != nil {
		return fmt.Errorf("stop the application before bootstrapping: %w", err)
	}
	defer store.Close()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(c.DataDir, "metadata.sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()
	var password []byte
	if *stdin {
		password, err = io.ReadAll(io.LimitReader(os.Stdin, 1027))
		password = []byte(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	} else {
		fmt.Fprint(os.Stderr, "Password (15+ characters): ")
		password, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("secure terminal input unavailable; use -password-stdin with a private pipe or file")
		}
		fmt.Fprint(os.Stderr, "Confirm password: ")
		confirmation, confirmErr := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if confirmErr != nil {
			return confirmErr
		}
		if string(password) != string(confirmation) {
			return fmt.Errorf("passwords do not match")
		}
	}
	if err != nil {
		return err
	}
	service, err := auth.New(db, c.SessionTTL, c.SessionIdleTTL, c.SecureCookies)
	if err != nil {
		return err
	}
	user, err := service.Bootstrap(ctx, *username, string(password))
	if err != nil {
		return err
	}
	fmt.Printf("Created initial administrator %q. Existing documents are assigned to this account.\n", user.Username)
	return nil
}
