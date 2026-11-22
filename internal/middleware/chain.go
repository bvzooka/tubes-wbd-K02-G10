// Package middleware berisi middleware HTTP umum yang dipasang di semua request
package middleware

import "net/http"

// Middleware membungkus sebuah handler untuk urusan yang sama di banyak route, misalnya log atau header keamanan
type Middleware = func(http.Handler) http.Handler

// Chain menyusun beberapa middleware jadi satu, yang pertama di daftar jadi lapisan paling luar
func Chain(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		// Dari belakang biar middleware belakang duluan
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
