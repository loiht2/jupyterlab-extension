package main

import (
	switcher "backend-handler/notebook-switcher"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Message represents the expected JSON payload
// Adjust fields according to the message structure from the other application.
type Message struct {
	NotifyGPUNeeded   string `json:"NotifyGPUNeeded"`
	NotifyGPUReleased string `json:"NotifyGPUReleased"`
	PodName           string `json:"PodName"`
	PodNamespace      string `json:"PodNamespace"`
}

func RealName(s string) string {
	i := strings.LastIndex(s, "-")
	if i <= 0 { // không có '-' hoặc '-' đứng đầu
		return s
	}
	return s[:i]
}

func messageHandler(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight request
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Only POST method is allowed"))
		return
	}

	// Ensure the body is closed after reading
	defer r.Body.Close()

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Parse JSON payload into Message struct
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		log.Printf("Error unmarshaling JSON: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid JSON payload"))
		return
	}

	// Process Notebook
	NotebookName := RealName(msg.PodName)
	response := map[string]string{}

	if msg.NotifyGPUNeeded == "true" {
		NewNotebookPodName, err := switcher.SwitcherToGPU(NotebookName, msg.PodNamespace)
		if err != nil {
			log.Printf("%v", err)
		}
		NewNotebookName := RealName(NewNotebookPodName)
		ns := url.PathEscape(msg.PodNamespace)
		nb := url.PathEscape(NewNotebookName)
		newURL := fmt.Sprintf("/notebook/%s/%s/", ns, nb)
		// Send a response back
		response = map[string]string{"status": "received", "podNamespace": msg.PodNamespace, "newNBName": NewNotebookName, "newURL": newURL}
		// Process the message (for now, just log it)
		log.Printf("Received message: NotifyGPUNeeded=%v, namespace=%v, newNBName=%v, newURL=%v", msg.NotifyGPUNeeded, msg.PodNamespace, NewNotebookName, newURL)
	}

	if msg.NotifyGPUReleased == "true" {
		NewNotebookPodName, err := switcher.SwitcherToCPU(NotebookName, msg.PodNamespace)
		if err != nil {
			log.Printf("%v", err)
		}
		NewNotebookName := RealName(NewNotebookPodName)
		ns := url.PathEscape(msg.PodNamespace)
		nb := url.PathEscape(NewNotebookName)
		p := path.Join("/notebook", ns, nb)       // standardize '/'
		newURL := strings.TrimRight(p, "/") + "/" // ensure exactly 1 '/'
		// Send a response back
		response = map[string]string{"status": "received", "podNamespace": msg.PodNamespace, "newNBName": NewNotebookName, "newURL": newURL}
		// Process the message (for now, just log it)
		log.Printf("Received message: NotifyGPUReleased=%v, namespace=%v, newNBName=%v, newURL=%v", msg.NotifyGPUReleased, msg.PodNamespace, NewNotebookName, newURL)
	}

	respBytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("Error marshaling response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)

}

func main() {
	// Register the handler for /messages endpoint
	http.HandleFunc("/messages", messageHandler)

	// Start the HTTP server on port 8080
	log.Println("Starting server on :8080, listening for POST messages at /messages")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
