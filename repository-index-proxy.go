package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

var DB_PATH string

func main() {
	DB_PATH = os.Getenv("DB_PATH")

	if DB_PATH == "" {
		log.Fatal("DB_PATH environment is not present.")
	}

	connection := fmt.Sprintf("file:%s?mode=ro&cache=shared", DB_PATH)

	db, err := sql.Open("sqlite3", connection)

	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	var version string
	err = db.QueryRow("SELECT SQLITE_VERSION()").Scan(&version)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(version)
}
