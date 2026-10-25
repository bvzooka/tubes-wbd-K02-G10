package httpx

import (
	"net/http"
	"net/url"
	"strconv"
	"dbplatform/internal/domain"
)

func ParsePage(r *http.Request) domain.PageRequest {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	return domain.PageRequest{Page: page, PageSize: size}.Normalize()
}

type Pager struct {
	Page int
	TotalPages int
	HasPrev bool
	HasNext bool
	PrevURL string
	NextURL string
}

func NewPager(r *http.Request, req domain.PageRequest, hasNext bool) Pager {
	req = req.Normalize()
	link := func(page int) string {
		q := url.Values{}
		for k, v := range r.URL.Query() {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("size", strconv.Itoa(req.PageSize))
		return r.URL.Path + "?" + q.Encode()
	}
	p := Pager{Page: req.Page, HasPrev: req.Page > 1, HasNext: hasNext}
	if !hasNext {
		p.TotalPages = req.Page
	}
	if p.HasPrev {
		p.PrevURL = link(req.Page - 1)
	}
	if hasNext {
		p.NextURL = link(req.Page + 1)
	}
	return p
}
