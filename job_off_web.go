//go:build nodb && !noweb

package main

import "time"

func (s *Server) jobHost() *jobHost            { return nil }
func (s *Server) jobsBoot(workers int)         {}
func (s *Server) jobsShutdown(d time.Duration) {}
