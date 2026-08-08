package graphql

import (
	"fmt"
	"strconv"
)

func parsePositiveInt64ID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("ID must be a positive decimal string")
	}
	return id, nil
}
