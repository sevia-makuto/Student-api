package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type Student struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type StudentInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type APIError struct {
	Error string `json:"error"`
}

var (
	students = make(map[int]Student)
	nextID   = 1
	mu       sync.RWMutex
)

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, APIError{Error: message})
}

func decodeInput(r *http.Request) (StudentInput, error) {
	var input StudentInput

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&input); err != nil {
		return input, err
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return input, errors.New("request must contain one JSON object")
	}

	return input, nil
}

func validateInput(input StudentInput) string {
	if strings.TrimSpace(input.Name) == "" {
		return "name is required"
	}
	if strings.TrimSpace(input.Email) == "" {
		return "email is required"
	}
	if len(input.Name) > 100 || len(input.Email) > 200 {
		return "name or email is too long"
	}
	if !strings.Contains(input.Email, "@") {
		return "email must contain @"
	}
	return ""
}

func listStudents(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	result := make([]Student, 0, len(students))
	for _, student := range students {
		result = append(result, student)
	}
	mu.RUnlock()

	writeJSON(w, http.StatusOK, result)
}

func createStudent(w http.ResponseWriter, r *http.Request) {
	input, err := decodeInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if message := validateInput(input); message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	for _, student := range students {
		if strings.EqualFold(student.Email, strings.TrimSpace(input.Email)) {
			writeError(w, http.StatusConflict, "email already exists")
			return
		}
	}

	student := Student{
		ID:    nextID,
		Name:  strings.TrimSpace(input.Name),
		Email: strings.TrimSpace(input.Email),
	}
	students[nextID] = student
	nextID++

	writeJSON(w, http.StatusCreated, student)
}

func getStudent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	mu.RLock()
	student, found := students[id]
	mu.RUnlock()

	if !found {
		writeError(w, http.StatusNotFound, "student not found")
		return
	}
	writeJSON(w, http.StatusOK, student)
}

func replaceStudent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	input, err := decodeInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if message := validateInput(input); message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if _, found := students[id]; !found {
		writeError(w, http.StatusNotFound, "student not found")
		return
	}
	for otherID, student := range students {
		if otherID != id &&
			strings.EqualFold(student.Email, strings.TrimSpace(input.Email)) {
			writeError(w, http.StatusConflict, "email already exists")
			return
		}
	}

	student := Student{
		ID:    id,
		Name:  strings.TrimSpace(input.Name),
		Email: strings.TrimSpace(input.Email),
	}
	students[id] = student
	writeJSON(w, http.StatusOK, student)
}

func patchStudent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	var changes map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&changes); err != nil || changes == nil {
		writeError(w, http.StatusBadRequest, "invalid JSON object")
		return
	}

	for key := range changes {
		if key != "name" && key != "email" {
			writeError(w, http.StatusBadRequest, "unknown field: "+key)
			return
		}
	}

	mu.Lock()
	defer mu.Unlock()

	student, found := students[id]
	if !found {
		writeError(w, http.StatusNotFound, "student not found")
		return
	}

	if raw, exists := changes["name"]; exists {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			writeError(w, http.StatusBadRequest, "name must be a string")
			return
		}
		if strings.TrimSpace(value) == "" || len(value) > 100 {
			writeError(w, http.StatusBadRequest, "invalid name")
			return
		}
		student.Name = strings.TrimSpace(value)
	}

	if raw, exists := changes["email"]; exists {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			writeError(w, http.StatusBadRequest, "email must be a string")
			return
		}
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 200 || !strings.Contains(value, "@") {
			writeError(w, http.StatusBadRequest, "invalid email")
			return
		}
		for otherID, other := range students {
			if otherID != id && strings.EqualFold(other.Email, value) {
				writeError(w, http.StatusConflict, "email already exists")
				return
			}
		}
		student.Email = value
	}

	students[id] = student
	writeJSON(w, http.StatusOK, student)
}

func deleteStudent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if _, found := students[id]; !found {
		writeError(w, http.StatusNotFound, "student not found")
		return
	}
	delete(students, id)
	w.WriteHeader(http.StatusNoContent)
}

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid student ID")
		return 0, false
	}
	return id, true
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /students", listStudents)
	mux.HandleFunc("POST /students", createStudent)
	mux.HandleFunc("GET /students/{id}", getStudent)
	mux.HandleFunc("PUT /students/{id}", replaceStudent)
	mux.HandleFunc("PATCH /students/{id}", patchStudent)
	mux.HandleFunc("DELETE /students/{id}", deleteStudent)

	handler := loggingMiddleware(mux)

	log.Println("Student API running on http://localhost:8090")
	log.Fatal(http.ListenAndServe(":8090", handler))
}
