package milvus

import (
	"strconv"
	"strings"
)

func collectionDimensionForBase(collectionName, baseName string) (int, bool) {
	prefix := strings.TrimSpace(baseName) + "_"
	if !strings.HasPrefix(collectionName, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(collectionName, prefix)
	dimension, err := strconv.Atoi(suffix)
	if err != nil || dimension <= 0 || suffix != strconv.Itoa(dimension) {
		return 0, false
	}
	return dimension, true
}

func matchesDimensionCollection(collectionName, baseName string) bool {
	_, ok := collectionDimensionForBase(collectionName, baseName)
	return ok
}
