package access

import "dbplatform/internal/domain"

// MaxLevel mengembalikan level tertinggi (View + Edit mengalahkan View Only).
// Nilai yang tidak dikenal diabaikan; tanpa level valid hasilnya AccessNone.
func MaxLevel(levels ...domain.AccessLevel) domain.AccessLevel {
	best := domain.AccessNone
	for _, l := range levels {
		if l.Rank() > best.Rank() {
			best = l
		}
	}
	return best
}
