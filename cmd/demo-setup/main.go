package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"qag/internal/demo"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	c, secrets, err := demo.DatabaseConfig(os.Getenv("QAG_DATABASE_URL"), os.Getenv("QAG_DEMO_DATABASE_URL"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("QAG_DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("БД для подготовки demo-роли недоступна")
	}
	defer pool.Close()
	if err = demo.PrepareRole(ctx, pool, c, secrets); err != nil {
		return err
	}
	fmt.Println("Демонстрационная роль подготовлена")
	return nil
}
