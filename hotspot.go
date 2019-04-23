package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc/eventlog"
	"gopkg.in/routeros.v2"
)

var (
	address    = flag.String("address", "192.168.11.1:8728", "Address")
	username   = flag.String("username", "admin", "Username")
	password   = flag.String("password", "78758", "Password")
	properties = flag.String("properties", "name", "Properties")
	interval   = flag.Duration("interval", 1*time.Minute, "Interval")
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

func getGuests() ([]Guest, error) {
	name := os.Getenv("hotspot_profile_name")
	ip := os.Getenv("hotspot_remote_ip")
	safename := url.QueryEscape(name)
	url := fmt.Sprint("http://", ip, ":8080/?name=", safename)

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
func getHotspotUsers() ([]user, error) {
	flag.Parse()
	var users []user
	c, err := routeros.Dial(*address, *username, *password)
	if err != nil {
		log.Print(err)
		return nil, err
	}
	reply, err := c.Run("/ip/hotspot/user/print", "?disabled=false")
	if err != nil {
		log.Print(err)
		return nil, err
	}
	for _, re := range reply.Re {
		if re.Map["name"] != "default-trial" {
			users = append(users, user{id: re.Map[".id"], name: re.Map["name"]})
		}
	}
	return users, nil
}

//Atol is a function for changing Turkish characters to english
func Atol(word string) string {
	var replacer = strings.NewReplacer("İ", "I", "Ü", "U", "Ğ", "G", "Ş", "S", "Ö", "O", "Ç", "C")
	return replacer.Replace(word)
}
func deleteHotspotUsers(users []user) error {
	flag.Parse()
	c, err := routeros.Dial(*address, *username, *password)
	if err != nil {
		log.Print(err.Error())
		return err
	}
	for _, row := range users {
		_, err := c.Run("/ip/hotspot/user/remove", "=.id="+row.id)
		if err != nil {
			log.Print(err.Error())
			return err
		}
	}
	return nil
}
func createHotspotUsers(users []user) error {
	flag.Parse()
	c, err := routeros.Dial(*address, *username, *password)
	if err != nil {
		log.Print(err.Error())
		return err
	}
	for _, row := range users {
		_, err := c.Run("/ip/hotspot/user/add", "=name="+row.name, "=password="+row.password, "=comment="+Atol(row.comment), "=profile="+row.profile)
		if err != nil {
			log.Print(err.Error())
			return err
		}
	}
	return nil
}
func start() {
	const name = "hotspot-sync-win"

	elog, err := eventlog.Open(name)
	if err != nil {
		return
	}
	defer elog.Close()

	dir, err := os.Getwd()
	if err != nil {
		return
	}
	separator := "/"
	if runtime.GOOS == "windows" {
		separator = "\\"
	}
	filename := fmt.Sprint(dir, separator, "hotspot-sync.log")
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		elog.Info(1, fmt.Sprintf("error opening file: %v", err))
	}
	defer f.Close()
	log.SetOutput(f)
	for {
		log.Printf("Sync started...")
		guests, err := getGuests()
		if err == nil {
			elog.Info(1, fmt.Sprintf("Number of guests = %d", len(guests)))
		} else {
			elog.Info(1, "Connection fail.\nCould get data from hotel api server.")
			time.Sleep(*interval)
			continue
		}
		users, err := getHotspotUsers()
		if err == nil {
			elog.Info(1, fmt.Sprintf("Number of hotspot users = %d", len(users)))
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
			if iuser.name != "odeon" {
				if delete {
					deletelist = append(deletelist, iuser)
				} else {
					guests = append(guests[:deleteid], guests[deleteid+1:]...)
				}
			}
		}
		var createlist []user
		for _, iguest := range guests {
			createlist = append(createlist, user{name: iguest.ID, password: strconv.Itoa(iguest.BirthYear), comment: iguest.Name + " Check in & out date: " + iguest.CheckInDate.Format("Jan-02-2006") + " - " + iguest.CheckOutDate.Format("Jan-02-2006"), profile: "uprof_customer"})
		}
		elog.Info(1, fmt.Sprintf("How many hotspot users need to be delete ? : %d\n", len(deletelist)))
		for _, row := range deletelist {
			log.Printf("Comment\t,Id\n")
			log.Printf("%s\t%s\n", row.comment, row.name)
		}
		elog.Info(1, fmt.Sprintf("How many hotspot users need to be inserted ? : %d\n", len(createlist)))
		for _, row := range createlist {
			log.Printf("Comment\t,Id\n")
			log.Printf("%s\t%s\n", row.comment, row.name)
		}
		deleteHotspotUsers(deletelist)
		createHotspotUsers(createlist)
		time.Sleep(*interval)
	}
}

func serve(closesignal chan int) {
	go func() {
		start()
	}()
	<-closesignal
}
