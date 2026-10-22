package domain

// AccessLevel adalah tingkat akses sebuah group (dan user) pada sebuah database.
type AccessLevel string

const (
	AccessNone     AccessLevel = ""
	AccessViewOnly AccessLevel = "view_only"
	AccessViewEdit AccessLevel = "view_edit"
)

// Rank mengembalikan urutan kekuatan akses: 0 tidak ada, 1 view only, 2 view edit.
func (l AccessLevel) Rank() int {
	switch l {
	case AccessViewOnly:
		return 1
	case AccessViewEdit:
		return 2
	default:
		return 0
	}
}

// Valid true jika level adalah salah satu level yang boleh disimpan.
func (l AccessLevel) Valid() bool { return l.Rank() > 0 }

// AtLeast true jika level ini minimal sekuat min. AccessNone tidak pernah memenuhi apa pun.
func (l AccessLevel) AtLeast(min AccessLevel) bool {
	return l.Rank() > 0 && l.Rank() >= min.Rank()
}
