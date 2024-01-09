package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
)

var DB_PATH string
var DB *sql.DB

// OrderedMap represents a map with ordered keys.
type OrderedMap struct {
	keys []string
	data map[string]interface{}
}

func newOrderedMap() *OrderedMap {
	return &OrderedMap{
		data: make(map[string]interface{}),
	}
}

func (om *OrderedMap) set(key string, value interface{}) {
	om.keys = append(om.keys, key)
	om.data[key] = value
}

func (om *OrderedMap) MarshalJSON() ([]byte, error) {
	buf := bytes.NewBufferString("{")
	for i, key := range om.keys {
		if i != 0 {
			buf.WriteString(",")
		}
		value := om.data[key]
		jsonValue, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		buf.WriteString(fmt.Sprintf(`"%s":%s`, key, jsonValue))
	}
	buf.WriteString("}")
	return buf.Bytes(), nil
}

func queryIndexes(db *sql.DB, query string) ([]byte, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Get column names and types from the result set.
	columnNames, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []*OrderedMap

	// Loop through the result set.
	for rows.Next() {
		values := make([]interface{}, len(columnNames))

		valuePointers := make([]interface{}, len(columnNames))
		for i := range values {
			valuePointers[i] = &values[i]
		}

		if err := rows.Scan(valuePointers...); err != nil {
			return nil, err
		}

		rowData := newOrderedMap()

		for i, columnName := range columnNames {
			rowData.set(columnName, values[i])
		}

		results = append(results, rowData)

	}

	// Convert the results slice to a JSON array.
	jsonData, err := json.Marshal(results)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

func validateSearchValue(pkg_search string) error {
	if len(pkg_search) > 50 {
		return errors.New("package' length can not be greater than 50.")
	}

	if len(pkg_search) > 0 {
		pkgNameRegex := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

		if !pkgNameRegex.MatchString(pkg_search) {
			return errors.New("Package name can only contain English alphabets, numbers, '-' and '_' characters.")
		}
	}

	return nil
}

func queryEndpoint(w http.ResponseWriter, r *http.Request) {
	responseCh := make(chan []byte)

	go func() {
		w.Header().Set("Content-Type", "application/json")

		pkg_search := r.URL.Query().Get("package")

		err := validateSearchValue(pkg_search)
		if err != nil {
			r := make(map[string]interface{})
			r["error"] = err.Error()
			err, _ := json.Marshal(r)
			w.WriteHeader(http.StatusBadRequest)
			responseCh <- err
			return
		}

		// SAFETY: pkg_search param is validated through a regex pattern in validateSearchValue function. 
		query := fmt.Sprintf(`
               SELECT
               name, v_readable as version, description, arch, kind, tags, installed_size as "installed size", maintainer, license, source_repository as "repository", mandatory_dependencies as "dependencies"
               FROM repository
               WHERE name LIKE '%%%s%%'
               ORDER BY index_timestamp DESC
               LIMIT 150;`, pkg_search)

		jsonData, err := queryIndexes(DB, query)
		if err != nil {
			r := make(map[string]interface{})
			r["error"] = err.Error()
			err, _ := json.Marshal(r)

			w.WriteHeader(http.StatusBadRequest)
			responseCh <- err
			return
		}

		contentLength := strconv.Itoa(len(jsonData))
		w.Header().Set("Content-Length", contentLength)
		w.WriteHeader(http.StatusOK)

		responseCh <- jsonData
	}()

	select {
	case response := <-responseCh:
		w.Write(response)
	case <-time.After(5 * time.Second): // timeout handling
		w.WriteHeader(http.StatusRequestTimeout)
		w.Write([]byte("Timeout exceeded"))
	}

}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (grw gzipResponseWriter) Write(b []byte) (int, error) {
	return grw.Writer.Write(b)
}

func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// only compress if client supports gzip encoding
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")

			w.Header().Set("Access-Control-Allow-Origin", "*")

			gzipWriter := gzip.NewWriter(w)
			defer gzipWriter.Close()

			// replace the response writer
			w = gzipResponseWriter{Writer: gzipWriter, ResponseWriter: w}
		}

		// move to next handler
		next.ServeHTTP(w, r)
	})
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("API is healthy"))
}

func main() {
	DB_PATH = os.Getenv("DB_PATH")
	apiPort := os.Getenv("API_PORT")

	if DB_PATH == "" {
		log.Fatal("DB_PATH environment is not present.")
	}

	if apiPort == "" {
		apiPort = "8126"
	}

	connection := fmt.Sprintf("file:%s?mode=ro&cache=shared", DB_PATH)

	var err error
	DB, err = sql.Open("sqlite3", connection)

	if err != nil {
		log.Fatal(err)
	}

	defer DB.Close()

	mux := mux.NewRouter().StrictSlash(true)

	mux.HandleFunc("/", queryEndpoint)
	mux.HandleFunc("/health", healthCheckHandler)

	// Apply the gzip middleware to the entire mux
	handler := middleware(mux)

	fmt.Printf("package-search-api is listening on port %s for %s\n", apiPort, DB_PATH)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", apiPort), handler))

}
