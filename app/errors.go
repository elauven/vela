package app

import (
	"errors"
	"fmt"
	"net/http"
)

// RedirectError is the error of Redirect.
type RedirectError struct {
	Status   int
	Location string
}

func (e *RedirectError) Error() string {
	return fmt.Sprintf("redirect %d to %s", e.Status, e.Location)
}

// StatusError is the error of NotFound and Error: a status with a message
// that the error page shows.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%d %s", e.Status, e.Message)
}

// Redirect returns the error a loader returns to redirect (docs/APP.md
// L-5). A document request gets status (301, 302, 303, 307 or 308; 303 when
// another one is given) and the Location header.
func Redirect(status int, location string) error {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		status = http.StatusSeeOther
	}
	return &RedirectError{Status: status, Location: location}
}

// NotFound returns the error a loader returns for a missing resource: 404.
func NotFound() error {
	return &StatusError{Status: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)}
}

// Error returns an error with a status and a message that the error page
// shows. A status outside 400–599 is 500.
func Error(status int, message string) error {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	return &StatusError{Status: status, Message: message}
}

// PanicError is a panic of a loader. It is logged and answered with 500.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Value)
}

// ErrNoParent is returned by Parent when no layout above has a loader.
var ErrNoParent = errors.New("app: no layout with a loader above this level")
