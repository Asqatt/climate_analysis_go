package main

import (
	"crypto/rand"
	"database/sql"
	_ "database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	conv "strconv"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"github.com/mmcloughlin/geohash"
)

const (
	DefaultSize = 15
)

//Cache is a glaobal variable keeps a short list of query resualts in memory
type Cache map[string]*Node

//Node is a cache node
type Node struct {
	key  string
	val  []Weather
	next *Node
	prev *Node
}

type ItemSlice []Item

func (w ItemSlice) Len() int { fmt.Println("worked in sorting"); return len(w) }
func (w ItemSlice) Less(i, j int) bool {
	a, _ := conv.Atoi(w[i].Category[:4])
	b, _ := conv.Atoi(w[j].Category[:4])
	return a < b
}
func (w ItemSlice) Swap(i, j int) { w[i], w[j] = w[j], w[i] }

func sortItem(w []Item) {
	sort.Sort(ItemSlice(w))
}

var head *Node
var tail *Node
var csize int

func (c Cache) evict() *Node {

	item := head.next
	delete(c, item.key)
	c.pop(item)
	return item
}

func (c Cache) pop(item *Node) {
	item.next.prev = item.prev
	item.prev.next = item.next
}

func (c Cache) push(item *Node) {
	tail.prev.next = item
	item.prev = tail.prev
	tail.prev = item
	item.next = tail
}

func init() {
	head = new(Node)
	tail = new(Node)
	head.next = tail
	tail.prev = head
	csize = DefaultSize
}

//Set puts certain value in the cache or update if it already exists
func (c Cache) Set(key string, val []Weather) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	item := c[key]
	if item == nil {
		if len(c) == csize {
			item = c.evict()
		} else {
			item = new(Node)
		}
		item.key = key
		item.val = val
		c.push(item)
		c[key] = item
	} else {
		item.val = val
		if tail.prev != item {
			c.pop(item)
			c.push(item)
		}
	}
}

//Get returns cache element and updates the cache in terms of this query
func (c Cache) Get(key string) ([]Weather, bool) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	item, ok := c[key]
	if !ok {
		return nil, false
	}
	if tail.prev != item {
		c.pop(item)
		c.push(item)
	}
	return item.val, true
}

//Range iterates all key values using provided function in order of LRU
func (c Cache) Range(f func(key interface{}, value interface{}) bool) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	if item := tail; item != nil {
		item = tail.prev
		for item != head {
			if !f(item.key, item.val) {
				return
			}
			item = item.prev
		}
	}
}

//CacheUpdater updates cache : keeps cache in appropriate size
func (c Cache) CacheUpdater(updates <-chan struct{}) {
	//This method haven't implemented , Used another level of cache
	for {
		select {
		case <-updates:
			fmt.Printf("This is cache length %d", len(c))
		}
	}

}

//Sessions keeps track of current user's query reuslts in the cache
type Sessions map[string]string

//NewSessions initiates a global Sessions variable
func NewSessions() *Sessions {
	s := make(map[string]string)
	k := Sessions(s)
	return &k
}

func (s Sessions) GenerateNewSession(value string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	key := base64.URLEncoding.EncodeToString(b)
	sessionRWMutex.Lock()
	defer sessionRWMutex.Unlock()
	s[key] = value
	return key
}

func (s Sessions) GetSessionValue(cookie string) (string, bool) {
	sessionRWMutex.RLock()
	defer sessionRWMutex.RUnlock()
	if val, ok := s[cookie]; ok {
		return val, true
	}
	return "", false
}
func (s Sessions) DestroySession(value string) {
	sessionRWMutex.Lock()
	delete(s, value)
	sessionRWMutex.Unlock()
}
func (s Sessions) UpdateSession(value string) {
	sessionRWMutex.Lock()
	defer sessionRWMutex.Unlock()
	s[value] = value
}
func NewCache() *Cache {
	c := make(map[string]*Node)
	k := Cache(c)
	return &k
}

var (
	templates *template.Template

	percision = 4

	cacheSize = 3

	cache = NewCache()

	sessions = NewSessions()

	sessionRWMutex sync.RWMutex

	cacheMutex sync.Mutex

	updater chan struct{}
)

type Station struct {
	Id        string
	Latitude  float64
	Longitude float64
	Hash      string
}

type Weather struct {
	id         string
	stationid  string
	date       string
	winddirect int

	maxwindspeed float32
	minwindspeed float32
	avgwindspeed float32

	maxcloudheight float32
	mincloudheight float32
	avgcloudheight float32

	maxvisibility int
	minvisibility int
	avgvisibility int

	maxairtemperature float32
	minairtemperature float32
	avgairtemperature float32

	maxdewtemperature float32
	mindewtemperature float32
	avgdewtemperature float32

	maxairpressure float32
	minairpressure float32
	avgairpressure float32
}
type Service struct {
	db *sql.DB
}

func main() {

	// Configure the database connection (always check errors)
	db, err := sql.Open("mysql", "remote:remote@(192.168.188.1:3306)/globalweather")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	s := &Service{db: db}

	templates = template.Must(template.ParseGlob("../resources/static/*.html"))
	updater = make(chan struct{}, DefaultSize)
	go cache.CacheUpdater(updater)

	r := mux.NewRouter()
	r.Handle("/", http.HandlerFunc(handleIndexGet)).Methods("GET")
	r.HandleFunc("/airTemperatureYear", cache.handleAirTemperatureAnnually).Methods("GET")
	r.HandleFunc("/dewTemperatureYear", cache.handleDewTemperatureAnnually).Methods("GET")
	r.HandleFunc("/windSpeedYear", cache.handleDewTemperatureAnnually).Methods("GET")
	r.HandleFunc("/cloudHeightYear", cache.handleDewTemperatureAnnually).Methods("GET")
	r.HandleFunc("/airPressureYear", cache.handleAirPressureAnnually).Methods("GET")
	r.HandleFunc("/visibilityYear", cache.handleVisibilityAnnually).Methods("GET")
	r.HandleFunc("/stationsearch", s.handleIndexPost).Methods("POST")
	fs := http.FileServer(http.Dir("../resources/static/")) //file server
	r.PathPrefix("/{js|css|json|img}/").Handler(http.StripPrefix("", fs))
	log.Fatal(http.ListenAndServe(":8080", r))

}

func validateSession(req *http.Request) (string, bool) {
	cookie, err := req.Cookie("s3cr3t")
	if err != nil || (*cookie).Value == "" {
		return "", false
	}
	stationid, ok := sessions.GetSessionValue(cookie.Value)
	if !ok || (*cookie).Value == "" {
		return "", false
	}
	return stationid, true
}

func (c Cache) handleAirTemperatureAnnually(w http.ResponseWriter, req *http.Request) {

	if stationid, ok := validateSession(req); ok {
		ws, _ := c.Get(stationid)
		annuals := processAirTemp(ws, 4)
		w.Header().Set("Content-Type", "application/json")

		fmt.Printf("Len of annuals %d\n", len(annuals))
		json.NewEncoder(w).Encode(annuals)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther) //status code 303
	}
}
func (c Cache) handleDewTemperatureAnnually(w http.ResponseWriter, req *http.Request) {
	if stationid, ok := validateSession(req); ok {
		ws, _ := c.Get(stationid)
		annuals := processDewTemp(ws, 4)
		w.Header().Set("Content-Type", "application/json")

		fmt.Printf("Len of annuals %d\n", len(annuals))
		json.NewEncoder(w).Encode(annuals)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther) //status code 303
	}

}
func (c Cache) handleWindSpeedAnnually(w http.ResponseWriter, req *http.Request) {
	if stationid, ok := validateSession(req); ok {
		ws, _ := c.Get(stationid)
		annuals := processWindSpeed(ws, 4)
		w.Header().Set("Content-Type", "application/json")

		fmt.Printf("Len of annuals %d\n", len(annuals))
		json.NewEncoder(w).Encode(annuals)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther) //status code 303
	}

}
func (c Cache) handleCloudHeightAnnually(w http.ResponseWriter, req *http.Request) {
	if stationid, ok := validateSession(req); ok {
		ws, _ := c.Get(stationid)
		annuals := processCloudHeight(ws, 4)
		w.Header().Set("Content-Type", "application/json")

		fmt.Printf("Len of annuals %d\n", len(annuals))
		json.NewEncoder(w).Encode(annuals)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther) //status code 303
	}

}

func (c Cache) handleAirPressureAnnually(w http.ResponseWriter, req *http.Request) {
	if stationid, ok := validateSession(req); ok {
		ws, _ := c.Get(stationid)
		annuals := processAirPressure(ws, 4)
		w.Header().Set("Content-Type", "application/json")

		fmt.Printf("Len of annuals %d\n", len(annuals))
		json.NewEncoder(w).Encode(annuals)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther) //status code 303
	}

}

func (c Cache) handleVisibilityAnnually(w http.ResponseWriter, req *http.Request) {
	if stationid, ok := validateSession(req); ok {
		ws, _ := c.Get(stationid) //session valid , ok to ignore
		annuals := processVisibility(ws, 4)
		w.Header().Set("Content-Type", "application/json")

		fmt.Printf("Len of annuals %d\n", len(annuals))
		json.NewEncoder(w).Encode(annuals)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther) //status code 303
	}

}

func handleIndexGet(w http.ResponseWriter, req *http.Request) {
	templates.ExecuteTemplate(w, "index.html", nil)
}
func (s *Service) handleIndexPost(w http.ResponseWriter, req *http.Request) {

	req.ParseForm()
	longitude, _ := conv.ParseFloat(req.PostForm.Get("longitude"), 64)
	latitude, _ := conv.ParseFloat(req.PostForm.Get("latitude"), 64) //safe to ignore error
	s.db.Ping()
	if s.db != nil {
		st := s.getNearestStation(latitude, longitude)
		if expiredToken, ok := validateSession(req); ok { // refresh session key
			sessions.DestroySession(expiredToken)
		}
		token := sessions.GenerateNewSession(st.Id)
		cookie := http.Cookie{Name: "s3cr3t", Value: token, Expires: time.Now().AddDate(0, 0, 1)}
		http.SetCookie(w, &cookie)
		ws := s.getWeatherAnnual(st.Id, "") //retirve all year climate

		cache.Set(st.Id, ws) //store in cache for subsequent requests
		cache.Range(func(key interface{}, val interface{}) bool {
			if k, ok := key.(string); ok {
				fmt.Printf("%s : %T \n", k, val)
			}
			return true
		})
		if updater != nil {
			fmt.Println("message sent to cache updater !")
			updater <- struct{}{}
		}

		// fmt.Errorf("Set cache %s with len %d \n", st.Id, len((*cache)[st.Id]))

	} else {
		fmt.Print("db is nil")
	}
	templates.ExecuteTemplate(w, "weather.html", nil)
}

type Item struct {
	Category string  `json:"category"`
	Max      float32 `json:"max"`
	Min      float32 `json:"min"`
	Avg      int     `json:"average"`
}

func processWindSpeed(whole []Weather, trimer int) []Item {
	var sum float32
	m := make(map[string][]Weather)
	for _, h := range whole {
		date := h.date[:trimer]
		m[date] = append(m[date], h)
	}
	annuals := make([]Item, 0, 15)
	for date, list := range m {
		temp := Item{``, -1000, +1000, 0}
		for _, w := range list {
			if w.maxwindspeed > temp.Max {
				temp.Max = w.maxwindspeed
			}
			if w.minwindspeed < temp.Min {
				temp.Min = w.minwindspeed
			}
			sum += w.avgwindspeed
		}
		temp.Category = date
		temp.Avg = int(sum / float32(len(list)))
		sum = 0.0
		annuals = append(annuals, temp)
	}
	sortItem(annuals)
	return annuals
}
func processCloudHeight(whole []Weather, trimer int) []Item {
	var sum float32
	m := make(map[string][]Weather)
	for _, h := range whole {
		date := h.date[:trimer]
		m[date] = append(m[date], h)
	}
	annuals := make([]Item, 0, 15)
	for date, list := range m {
		temp := Item{``, -1000, +1000, 0}
		for _, w := range list {
			if w.maxcloudheight > temp.Max {
				temp.Max = w.maxcloudheight
			}
			if w.mincloudheight < temp.Min {
				temp.Min = w.mincloudheight
			}
			sum += w.avgcloudheight
		}
		temp.Category = date
		temp.Avg = int(sum / float32(len(list)))
		sum = 0.0
		annuals = append(annuals, temp)
	}
	sortItem(annuals)
	return annuals
}
func processVisibility(whole []Weather, trimer int) []Item {
	var sum int
	m := make(map[string][]Weather)
	for _, h := range whole {
		date := h.date[:trimer]
		m[date] = append(m[date], h)
	}
	annuals := make([]Item, 0, 15)
	for date, list := range m {
		temp := Item{``, -1000, +1000, 0}
		for _, w := range list {
			if w.maxvisibility > int(temp.Max) {
				temp.Max = float32(w.maxvisibility)
			}
			if float32(w.minvisibility) < temp.Min {
				temp.Min = float32(w.minvisibility)
			}
			sum += w.avgvisibility
		}
		temp.Category = date
		temp.Avg = int(sum / len(list))
		sum = 0.0
		annuals = append(annuals, temp)
	}
	sortItem(annuals)
	return annuals
}

func processAirPressure(whole []Weather, trimer int) []Item {
	var sum float32
	m := make(map[string][]Weather)
	for _, h := range whole {
		date := h.date[:trimer]
		m[date] = append(m[date], h)
	}
	annuals := make([]Item, 0, 15)
	for date, list := range m {
		temp := Item{``, -1000, +1000, 0}
		for _, w := range list {
			if w.maxairpressure > temp.Max {
				temp.Max = w.maxairpressure
			}
			if w.minairpressure < temp.Min {
				temp.Min = w.minairpressure
			}
			sum += w.avgairpressure
		}
		temp.Category = date
		temp.Avg = int(sum / float32(len(list)))
		sum = 0.0
		annuals = append(annuals, temp)
	}
	sortItem(annuals)
	return annuals
}

//spli trims date 200908 to 2009/20090/200908
func processAirTemp(whole []Weather, trimer int) []Item {
	var sum float32
	m := make(map[string][]Weather)
	for _, h := range whole {
		date := h.date[:trimer]
		m[date] = append(m[date], h)
	}
	annuals := make([]Item, 0, 15)
	for date, list := range m {
		temp := Item{``, -1000, +1000, 0}
		for _, w := range list {
			if w.maxairtemperature > temp.Max {
				temp.Max = w.maxairtemperature
			}
			if w.minairtemperature < temp.Min {
				temp.Min = w.minairtemperature
			}
			sum += w.avgairtemperature
		}
		temp.Category = date
		temp.Avg = int(sum / float32(len(list)))
		sum = 0.0
		annuals = append(annuals, temp)
	}
	sortItem(annuals)
	return annuals
}
func processDewTemp(whole []Weather, trimer int) []Item {
	var sum float32
	m := make(map[string][]Weather)
	for _, h := range whole {
		date := h.date[:trimer]
		m[date] = append(m[date], h)
	}
	annuals := make([]Item, 0, 15)
	for date, list := range m {
		temp := Item{``, -1000, +1000, 0}
		for _, w := range list {
			if w.maxdewtemperature > temp.Max {
				temp.Max = w.maxdewtemperature
			}
			if w.mindewtemperature < temp.Min {
				temp.Min = w.mindewtemperature
			}
			sum += w.avgdewtemperature
		}
		temp.Category = date
		temp.Avg = int(sum / float32(len(list)))
		sum = 0.0
		annuals = append(annuals, temp)
	}
	sortItem(annuals)
	return annuals
}

func (s *Service) getWeatherAnnual(id, year string) []Weather {
	fmt.Println(id)
	stmt, err := s.db.Prepare(`select 	id  ,stationid, date  ,winddirect ,
								maxwindspeed ,minwindspeed ,avgwindspeed ,
								maxcloudheight ,mincloudheight ,avgcloudheight ,
								maxvisibility ,minvisibility ,avgvisibility ,
								maxairtemperature ,minairtemperature ,avgairtemperature ,
								maxdewtemperature ,mindewtemperature ,avgdewtemperature ,
								maxairpressure ,minairpressure ,avgairpressure 
								from weather where stationid = '` + id + `'and date regexp '` + year + "[0-9]{2}'")
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close() //return connection back to pool

	weathers := make([]Weather, 0, 16)
	results, err := stmt.Query()
	for results.Next() {
		var weather Weather
		results.Scan(&weather.id, &weather.stationid, &weather.date, &weather.winddirect,
			&weather.maxwindspeed, &weather.minwindspeed, &weather.avgwindspeed,
			&weather.maxcloudheight, &weather.mincloudheight, &weather.avgcloudheight,
			&weather.maxvisibility, &weather.minvisibility, &weather.avgvisibility,
			&weather.maxairtemperature, &weather.minairtemperature, &weather.avgairtemperature,
			&weather.maxdewtemperature, &weather.mindewtemperature, &weather.avgdewtemperature,
			&weather.maxairpressure, &weather.minairpressure, &weather.avgairpressure)

		weather.maxairtemperature, weather.minairtemperature, weather.avgairtemperature,
			weather.maxdewtemperature, weather.mindewtemperature, weather.avgdewtemperature =
			weather.maxairtemperature/10, weather.minairtemperature/10, weather.avgairtemperature/10,
			weather.maxdewtemperature/10, weather.mindewtemperature/10, weather.avgdewtemperature/10

		weathers = append(weathers, weather)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Len of the query of weather is %d\n", len(weathers))
	return weathers
}

func (s *Service) getNearestStation(latitude float64, longitude float64) Station {

	hash := geohash.EncodeWithPrecision(latitude, longitude, uint(percision))
	// fmt.Printf("This is hash :%s, this is query  %s \n", hash, "'"+hash[:2]+".{2}'")
	stmt, err := s.db.Prepare(`SELECT id,latitude,longitude,geohash FROM  station WHERE geohash regexp '` + hash[:2] + "';")
	if err != nil {
		log.Fatal(err, "prepare error")
	}
	defer stmt.Close()
	result, err := stmt.Query()
	if err != nil {
		log.Fatal(err, "Query error")
	}
	defer result.Close()
	stations := make([]Station, 0, 10)
	for result.Next() {
		var station Station
		result.Scan(&station.Id, &station.Latitude, &station.Longitude, &station.Hash)
		stations = append(stations, station)
	}
	fmt.Printf("Found %d station\n", len(stations))
	var station Station
	var min = latitude*latitude + longitude*longitude
	for _, st := range stations {
		deviation := (st.Latitude-latitude)*(st.Latitude-latitude) + (st.Longitude-longitude)*(st.Longitude-longitude)
		if deviation < min {
			min = deviation
			station = st
		}
	}
	return station
}
