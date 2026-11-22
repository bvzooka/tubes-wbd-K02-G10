package queue

import "time"

// RetryDelay: base, base*3, base*9, ... (5s, 15s, 45s untuk base 5s).
func RetryDelay(base time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 3
	}
	return d
}
