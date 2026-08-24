package handler

import "fmt"

// NormalizeUUID converts a 32-character hex string into a standard 36-character hyphenated UUID.
func NormalizeUUID(id string) string {
	if len(id) == 32 {
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			id[0:8], id[8:12], id[12:16], id[16:20], id[20:32])
	}
	return id
}
