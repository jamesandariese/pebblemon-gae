package pebblemon

import (
	"io"
	"net/http"
	"encoding/json"
	"errors"
	"fmt"
	"bytes"
	"appengine"
	"appengine/urlfetch"
	"appengine/datastore"
	"log"
)

var API_KEY string = "AIzaSyAAWkXHK9aRpWCDTws58_X0HIDDT0B7sVo"

type GCMSend struct {
	Data map[string]string `json:"data"`
	RegistrationIds []string `json:"registration_ids"`
	DryRun bool `json:"dry_run,omitempty"`
}

type GCMResponse struct {
	MulticastId int64 `json:"multicast_id"`
	Success int `json:"success"`
	Failure int `json:"failure"`
	CanonicalIds int `json:"canonical_ids"`
	Results []map[string]string `json:"results"`
}

type PebbleRegistration struct {
	RegistrationId string `json:"regid"`
	RestAuthToken string `json:"auth"`
}

type PebbleMessage struct {
	Title string `json:"title"`
	Message string `json:"message"`
	RestAuthToken string `json:"auth"`
}

func init() {
	http.HandleFunc("/register", register)
	http.HandleFunc("/send", send)
	//http.HandleFunc("/test", testmessage)
}

func readJSON(r io.Reader, v interface{}) error {
	bodyDecoder := json.NewDecoder(r)
	err := bodyDecoder.Decode(v)
	log.Printf("JSON Decoded: %#v\n", v)
	return err
}

func deleteRegistrationId(context appengine.Context, registrationId string) error {
        q := datastore.NewQuery("PebbleRegistration").
		Filter("RegistrationId =", registrationId).
		KeysOnly()
	
        keys, err := q.GetAll(context, nil)
        if err != nil {
                return err
	}
	return datastore.DeleteMulti(context, keys)
}

var ErrNoRegistrationFound = errors.New("No registration ID found for auth code")
var ErrSendGCMMessageFailed = errors.New("Failed to queue message in GCM")
var ErrSendGCMMessagePartiallyFailed = errors.New("Failed to queue some messages in GCM")

func sendGCMMessage(context appengine.Context, registrationIds []string, data map[string]string, dryRun bool) error {
	jsonMessage := &GCMSend{
		Data: data,
		RegistrationIds: registrationIds,
		DryRun: dryRun,
	}
	marshalledJson, err := json.Marshal(jsonMessage)
	if err != nil{
		return err
	}
	req, err := http.NewRequest("POST", "https://android.googleapis.com/gcm/send", bytes.NewBuffer(marshalledJson))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", fmt.Sprintf("key=%s", API_KEY))
	req.Header.Add("Content-Type", "application/json")

	client := urlfetch.Client(context)	
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	log.Printf("%#v: %#v\n", resp.Status, resp)
	var gcmResponse GCMResponse
	readJSON(resp.Body, &gcmResponse)
	
	if gcmResponse.Success == 0 {
		return ErrSendGCMMessageFailed
	} else if gcmResponse.Failure > 0 {
		for i := 0; i < len(gcmResponse.Results); i++ {
			if gcmError, ok := gcmResponse.Results[i]["error"]; ok {
				if gcmError == "InvalidRegistration" {
					log.Printf("Found invalid registration ID: %s\n", registrationIds[i])
					deleteRegistrationId(context, registrationIds[i])
				}
			}
		}
		return ErrSendGCMMessagePartiallyFailed
	}
	return nil
}

func sendMessage(context appengine.Context, restAuthKey, title, message string) error {
	registrationIds, err := registrationIdsForAuthKey(context, restAuthKey)
	if err != nil {
		return err
	}
	if registrationIds == nil {
		return ErrNoRegistrationFound
	}
	pebbleMessage := map[string]string{
		"title": title,
		"message": message,
	}
	return sendGCMMessage(context, registrationIds, pebbleMessage, false)
}



func setRegistrationIdForAuthKey(context appengine.Context, registrationId, auth string) (err error) {
	key := datastore.NewIncompleteKey(context, "PebbleRegistration", nil)
	_, err = datastore.Put(context, key, &PebbleRegistration{
		RegistrationId: registrationId,
		RestAuthToken: auth,
	})
	return
}

func registrationIdsForAuthKey(context appengine.Context, auth string) (registrationIds []string, err error) {
	
	q := datastore.NewQuery("PebbleRegistration").
		Filter("RestAuthToken =", auth)
	
	var registrations []PebbleRegistration
	_, err = q.GetAll(context, &registrations)
	if err != nil {
		return
	}
	for _, registration := range registrations {
		registrationIds = append(registrationIds, registration.RegistrationId)
	}
	return
}

func send(w http.ResponseWriter, r *http.Request) {
	context := appengine.NewContext(r)

	bodyDecoder := json.NewDecoder(r.Body)
	var message PebbleMessage
	bodyDecoder.Decode(&message)
	
	err := sendMessage(context, message.RestAuthToken, message.Title, message.Message)
	if err != nil {
		switch err {
		case ErrNoRegistrationFound:
			w.WriteHeader(404)
			fmt.Fprintf(w, "User not found\n")
		default:
			w.WriteHeader(500)
			fmt.Fprintf(w, "Unknown error\n")
			log.Printf("%#v", err)
		}
	}
}

func register(w http.ResponseWriter, r *http.Request) {
	context := appengine.NewContext(r)

	bodyDecoder := json.NewDecoder(r.Body)
	var registration PebbleRegistration
	bodyDecoder.Decode(&registration)
	//fmt.Fprintf(w, "%#v\n", registration)

	//enc := json.NewEncoder(w)

	err := sendGCMMessage(context, []string{registration.RegistrationId}, map[string]string{"test": "test"}, true)
	if err != nil {
		w.WriteHeader(400)
		fmt.Fprintf(w, "Invalid registration ID\n")
		return
	}
	err = setRegistrationIdForAuthKey(context, registration.RegistrationId, registration.RestAuthToken)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "Unknown error registering device\n")
		return
	}
}
