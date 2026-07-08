package main
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"
)

type updateRecord struct {
	Path string                 `json:"path"`
	Body map[string]interface{} `json:"body"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18443", "listen address")
	certFile := flag.String("cert", "", "TLS certificate file")
	keyFile := flag.String("key", "", "TLS key file")
	logFile := flag.String("log", "", "optional JSON log file for update requests")
	flag.Parse()

	if *certFile == "" || *keyFile == "" {
		log.Fatal("both --cert and --key are required")
	}

	var (
		mu      sync.Mutex
		records []updateRecord
	)

	persist := func() {
		if *logFile == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		file, err := os.Create(*logFile)
		if err != nil {
			log.Printf("write log file: %v", err)
			return
		}
		defer file.Close()
		if err := json.NewEncoder(file).Encode(records); err != nil {
			log.Printf("encode log file: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/UpdateService", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]interface{}{
			"Actions": map[string]interface{}{
				"#UpdateService.SimpleUpdate": map[string]interface{}{
					"target": "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
				},
			},
		})
	})
	mux.HandleFunc("/redfish/v1/UpdateService/FirmwareInventory", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]interface{}{
			"Members": []map[string]string{
				{"@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BMC"},
				{"@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BIOS"},
			},
		})
	})
	mux.HandleFunc("/redfish/v1/UpdateService/FirmwareInventory/BMC", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]interface{}{
			"@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BMC",
			"Id":        "BMC",
			"Name":      "BMC",
			"Version":   "nc.1.0.0-build1",
			"RelatedItem": []map[string]string{
				{"@odata.id": "/redfish/v1/Systems/x9000c3s7b1"},
			},
		})
	})
	mux.HandleFunc("/redfish/v1/UpdateService/FirmwareInventory/BIOS", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]interface{}{
			"@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BIOS",
			"Id":        "BIOS",
			"Name":      "BIOS",
			"Version":   "2.0.0",
			"RelatedItem": []map[string]string{
				{"@odata.id": "/redfish/v1/Systems/x9000c3s7b1"},
			},
		})
	})
	mux.HandleFunc("/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		records = append(records, updateRecord{Path: r.URL.Path, Body: body})
		mu.Unlock()
		persist()

		w.Header().Set("Location", "/redfish/v1/TaskService/Tasks/mock-task")
		w.WriteHeader(http.StatusAccepted)
	})

	log.Printf("mock Redfish server listening on https://%s", *addr)
	log.Fatal(http.ListenAndServeTLS(*addr, *certFile, *keyFile, mux))
}

func respondJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}