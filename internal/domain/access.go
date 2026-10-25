package domain

type AccessLevel string

const (
	AccessNone     AccessLevel = ""
	AccessViewOnly AccessLevel = "view_only"
	AccessViewEdit AccessLevel = "view_edit"
)

// Access level pakai angka/rank biar bisa bandingin antarlevel
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

func (l AccessLevel) Valid() bool { return l.Rank() > 0 }

// AtLeast selalu false untuk AccessNone, apa pun nilai min.
func (l AccessLevel) AtLeast(min AccessLevel) bool {
	return l.Rank() > 0 && l.Rank() >= min.Rank()
}
