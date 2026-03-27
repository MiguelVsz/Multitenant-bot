package main

import (
	"fmt"
	"log"
	"os"

	"multi-tenant-bot/internal/agents"

	"github.com/joho/godotenv"
)

func main() {
	// 1. Cargamos el archivo .env
	err := godotenv.Load(".env")
	if err != nil {
		log.Println("Aviso: No se pudo cargar el archivo .env, usando variables de sistema")
	}

	// 2. Iniciamos el chat
	if err := agents.RunConsoleChat(); err != nil {
		fmt.Fprintf(os.Stderr, "chat error: %v\n", err)
		os.Exit(1)
	}
}
