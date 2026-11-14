//go:build !nodb && !nopg

package main

import _ "github.com/lib/pq"

const hasPG = true
