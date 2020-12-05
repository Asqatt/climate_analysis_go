package main

import (
	"database/sql"
	_ "database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	conv "strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"github.com/mmcloughlin/geohash"
)

var templates *template.Template

var percision = 4

type Service struct {
	db *sql.DB
}

func main() {

	// Configure the database connection (always check errors)
	db, err := sql.Open("mysql", "remote:remote@(192.168.188.1:3306)/globalweather")
	if err != nil {
		panic(err)
	}
	// Initialize the first connection to the database, to see if everything works correctly.
	// Make sure to check the error.
	err = db.Ping()
	if err != nil {
		log.Fatal(err, "Can't ping")
	}

	defer db.Close()

	// See "Important settings" section.
	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	s := &Service{db: db}

	query := `
	select count(distinct id) from station
      ;`

	result, err := db.Exec(query)
	if err != nil {
		log.Fatal(err, "error on exec")
	}
	fmt.Println(result)

	templates = template.Must(template.ParseGlob("../resources/static/*.html"))

	r := mux.NewRouter()
	r.Handle("/", http.HandlerFunc(handleIndexGet)).Methods("GET")
	r.HandleFunc("/stationsearch", s.handleIndexPost).Methods("POST")
	fs := http.FileServer(http.Dir("../resources/static/")) //file server
	r.PathPrefix("/{js|css|json|img}/").Handler(http.StripPrefix("", fs))
	log.Fatal(http.ListenAndServe("localhost:8080", r))
}

func handleIndexGet(w http.ResponseWriter, req *http.Request) {
	templates.ExecuteTemplate(w, "index.html", nil)
}
func (s *Service) handleIndexPost(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	longitude, _ := conv.ParseFloat(req.PostForm.Get("longitude"), 64)
	latitude, _ := conv.ParseFloat(req.PostForm.Get("latitude"), 64) //safe to ignore error
	fmt.Println(latitude, longitude)
	hash := geohash.EncodeWithPrecision(latitude, longitude, uint(percision))
	fmt.Println(hash)
	if s.db != nil {
		fmt.Println("querying data")
		result, err := s.db.Exec(`SELECT * FROM  station WHERE geohash like '?%'`, hash[:3])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(result)
	} else {
		fmt.Print("db is nil")
	}
	// fmt.Println(geohash.Neighbors(hash))
	templates.ExecuteTemplate(w, "weather.html", nil)
}
