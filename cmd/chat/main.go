package main

import (
	"fmt"
	"os"

	"multi-tenant-bot/internal/agents"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	if err := agents.RunConsoleChat(); err != nil {
		fmt.Fprintf(os.Stderr, "chat error: %v\n", err)
		os.Exit(1)
	}
}
