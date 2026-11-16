//go:build !noweb && nodb

package main

import "net/http"

// sem banco de dados não há /ask
func (s *Server) serveAsk(w http.ResponseWriter, r *http.Request) bool { return false }
