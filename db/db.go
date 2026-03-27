package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

var DB *sql.DB

func Init() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL no está definida")
	}

	var err error
	DB, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Error abriendo DB: %v", err)
	}

	if err = DB.Ping(); err != nil {
		log.Fatalf("Error conectando a DB: %v", err)
	}

	fmt.Println("✅ Conexión a PostgreSQL establecida")
}
