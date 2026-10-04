package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
	"serviceops360/api/internal/config"
	"serviceops360/api/internal/database"
	"serviceops360/api/internal/repository"
	"serviceops360/api/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Bootstrap failed:", err)
		os.Exit(1)
	}
	fmt.Println("SUPER_ADMIN created.")
}

func run() error {
	if !term.IsTerminal(int(syscall.Stdin)) {
		return errors.New("run this command in an interactive terminal")
	}
	reader := bufio.NewReader(os.Stdin)
	name, err := readLine(reader, "Name: ")
	if err != nil {
		return err
	}
	if name == "" || len(name) > 200 {
		return errors.New("name must contain 1 to 200 characters")
	}
	emailInput, err := readLine(reader, "Email: ")
	if err != nil {
		return err
	}
	email, err := service.NormalizeEmail(emailInput)
	if err != nil {
		return errors.New("email is invalid")
	}
	password, err := readPassword("Password: ")
	if err != nil {
		return err
	}
	confirmation, err := readPassword("Confirm password: ")
	if err != nil {
		return err
	}
	if password != confirmation {
		return errors.New("passwords do not match")
	}
	passwordHash, err := service.HashPassword(password)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	return repository.NewAuthStore(pool).BootstrapSuperAdmin(ctx, name, email, passwordHash)
}

func readLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	value, err := reader.ReadString('\n')
	return strings.TrimSpace(value), err
}

func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	value, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	return string(value), err
}
