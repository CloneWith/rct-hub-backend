package paginate

// Params holds common pagination query parameters.
type Params struct {
	Page    int64 `form:"page"`
	PerPage int64 `form:"per_page"`
}

func (p *Params) Normalize() {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PerPage <= 0 || p.PerPage > 100 {
		p.PerPage = 20
	}
}

func (p *Params) Skip() int64 {
	return (p.Page - 1) * p.PerPage
}

// Result wraps a paginated list response.
type Result[T any] struct {
	Data       []T   `json:"data"`
	Page       int64 `json:"page"`
	PerPage    int64 `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

func NewResult[T any](data []T, params Params, total int64) Result[T] {
	params.Normalize()
	totalPages := total / params.PerPage
	if total%params.PerPage > 0 {
		totalPages++
	}
	return Result[T]{
		Data:       data,
		Page:       params.Page,
		PerPage:    params.PerPage,
		Total:      total,
		TotalPages: totalPages,
	}
}
