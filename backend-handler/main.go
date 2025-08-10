package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// Message represents the expected JSON payload
// Adjust fields according to the message structure from the other application.
type Message struct {
	Notify_gpu_needed   string `json:"Notify_gpu_needed"`
	Notify_gpu_released string `json:"Notify_gpu_released"`
	Pod_name            string `json:"Pod_name"`
	Ns                  string `json:"Ns"`
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

	response := map[string]string{}
	// Send a response back
	if msg.Notify_gpu_needed == "true" {
		response = map[string]string{"status": "received", "notify_gpu_needed": msg.Notify_gpu_needed}
		// Process the message (for now, just log it)
		log.Printf("Received message: notify_gpu_needed=%v, ns=%v, podName=%v", msg.Notify_gpu_needed, msg.Ns, msg.Pod_name)
	}
	if msg.Notify_gpu_released == "true" {
		response = map[string]string{"status": "received", "notify_gpu_released": msg.Notify_gpu_released}
		// Process the message (for now, just log it)
		log.Printf("Received message: notify_gpu_released=%v, ns=%v, podName=%v", msg.Notify_gpu_released, msg.Ns, msg.Pod_name)
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
