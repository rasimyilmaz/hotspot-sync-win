package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc/eventlog"
	"gopkg.in/routeros.v2"
)

var (
	properties      = flag.String("properties", "name", "Properties")
	settingFilename string
	interval        = time.Minute
	config          Configuration
)

//Guest is defined in house people in hotel
type Guest struct {
	CheckInDate  time.Time `json:"check_in_date"`
	CheckOutDate time.Time `json:"check_out_date"`
	IDCard       string    `json:"id_card"`
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	BirthYear    int       `json:"birth_year"`
}

type user struct {
	id       string
	name     string
	password string
	comment  string
	profile  string
}
type Configuration struct {
	Settings []Setting `json: "Settings"`
	Interval int       `json: "Interval"`
}

//Setting for application settings
type Setting struct {
	ServerAddress       string `json: "ServerAddress"`
	Profile             string `json: "Profile"`
	MikrotikAddress     string `json:"MikrotikAddress"`
	MikrotikUsername    string `json:"MikrotikUsername"`
	MikrotikPassword    string `json:"MikrotikPassword"`
	CustomerProfileName string `json:"CustomerProfileName"`
}

func getGuests(item Setting) ([]Guest, error) {
	url := fmt.Sprint("http://", item.ServerAddress, "/?name=", item.Profile)
	elog.Info(1, url+" "+settingFilename)
	// Build the request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Print("NewRequest: ", err)
		return nil, err
	}

	// For control over HTTP client headers,
	// redirect policy, and other settings,
	// create a Client
	// A Client is an HTTP client
	client := &http.Client{}

	// Send the request via a client
	// Do sends an HTTP request and
	// returns an HTTP response
	resp, err := client.Do(req)
	if err != nil {
		log.Print("Do: ", err)
		return nil, err
	}

	// Callers should close resp.Body
	// when done reading from it
	// Defer the closing of the body
	defer resp.Body.Close()

	// Fill the record with the data from the JSON
	var guests []Guest

	// Use json.Decode for reading streams of JSON data
	if err := json.NewDecoder(resp.Body).Decode(&guests); err != nil {
		return nil, err
	}
	return guests, nil
}
func getHotspotUsers(item Setting) ([]user, error) {
	flag.Parse()
	var users []user
	c, err := routeros.Dial(item.MikrotikAddress, item.MikrotikUsername, item.MikrotikPassword)
	if err != nil {
		return nil, err
	}
	reply, err := c.Run("/ip/hotspot/user/print")
	if err != nil {
		log.Print(err)
		return nil, err
	}

	for _, re := range reply.Re {
		if re.Map["name"] != "default-trial" {
			if re.Map["profile"] == item.CustomerProfileName {
				users = append(users, user{id: re.Map[".id"], name: re.Map["name"]})
			}
		}
	}
	return users, nil
}

//Atol is a function for changing Turkish characters to english
func Atol(word string) string {
	var replacer = strings.NewReplacer("İ", "I", "Ü", "U", "Ğ", "G", "Ş", "S", "Ö", "O", "Ç", "C")
	return replacer.Replace(word)
}
func deleteHotspotUsers(item Setting, users []user) error {
	flag.Parse()
	c, err := routeros.Dial(item.MikrotikAddress, item.MikrotikUsername, item.MikrotikPassword)
	if err != nil {
		return err
	}
	for _, row := range users {
		_, err := c.Run("/ip/hotspot/user/remove", "=.id="+row.id)
		if err != nil {
			return err
		}
	}
	return nil
}
func createHotspotUsers(item Setting, users []user) error {
	flag.Parse()
	c, err := routeros.Dial(item.MikrotikAddress, item.MikrotikUsername, item.MikrotikPassword)
	if err != nil {
		return err
	}
	for _, row := range users {
		_, err := c.Run("/ip/hotspot/user/add", "=name="+row.name, "=password="+row.password, "=comment="+Atol(row.comment), "=profile="+row.profile)
		if err != nil {
			return err
		}
	}
	return nil
}
func start() {
	const name = "HotspotSyncWin"

	elog, err := eventlog.Open(name)
	if err != nil {
		return
	}
	defer elog.Close()

	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatal(err)
	}
	if err != nil {
		return
	}
	logFilename := filepath.Join(dir, "hotspot-sync.log")
	settingFilename = filepath.Join(dir, "setting.json")
	f, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		elog.Info(1, fmt.Sprintf("error opening file: %v", err))
	}
	defer f.Close()
	log.SetOutput(f)
	for {
		elog.Info(1, "Sync started.Interval "+interval.String()+" minutes.")
		log.Printf("Sync started...")
		time.Sleep(interval)
		file, err := ioutil.ReadFile(settingFilename)
		if err != nil {
			log.Printf(err.Error())
			continue
		}
		err = json.Unmarshal([]byte(file), &config)
		if err != nil {
			log.Printf(err.Error())
			continue
		}
		interval = time.Duration(config.Interval) * time.Minute
		for _, item := range config.Settings {
			guests, err := getGuests(item)
			if err == nil {
				log.Printf(fmt.Sprintf("Number of guests = %d", len(guests)))
			} else {
				log.Printf(fmt.Sprintf("getGuests function call error : Connection fail.\nCould get data from hotel api server.%s", err.Error()))
				continue
			}
			users, err := getHotspotUsers(item)
			if err == nil {
				log.Printf("Number of hotspot users = %d", len(users))
			} else {
				log.Printf("getHotspotUsers function call error : " + err.Error())
				continue
			}
			var deletelist []user
			var delete bool
			var deleteid int
			for _, iuser := range users {
				delete = true
				for k, iguest := range guests {
					if iguest.ID == iuser.name {
						delete = false
						deleteid = k
					}
				}
				if delete {
					deletelist = append(deletelist, iuser)
				} else {
					guests = append(guests[:deleteid], guests[deleteid+1:]...)
				}
			}
			var createlist []user
			for _, iguest := range guests {
				createlist = append(createlist, user{name: iguest.ID, password: strconv.Itoa(iguest.BirthYear), comment: iguest.Name + " Check in & out date: " + iguest.CheckInDate.Format("Jan-02-2006") + " - " + iguest.CheckOutDate.Format("Jan-02-2006"), profile: "uprof_customer"})
			}
			log.Printf("How many hotspot users need to be delete ? : %d\n", len(deletelist))
			for _, row := range deletelist {
				log.Printf("Comment\t,Id\n")
				log.Printf("%s\t%s\n", row.comment, row.name)
			}
			log.Printf("How many hotspot users need to be inserted ? : %d\n", len(createlist))
			for _, row := range createlist {
				log.Printf("Comment\t,Id\n")
				log.Printf("%s\t%s\n", row.comment, row.name)
			}
			err = deleteHotspotUsers(item, deletelist)
			if err != nil {
				log.Print("deleteHotspotUsers function call error : " + err.Error())
				continue
			}
			createHotspotUsers(item, createlist)
			if err != nil {
				log.Print("createHotspotUsers function call error : " + err.Error())
			}
		}
	}
}

func serve(closesignal chan int) {
	go func() {
		start()
	}()
	<-closesignal
}
