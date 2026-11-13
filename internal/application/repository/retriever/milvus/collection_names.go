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
	dimension, err := strconv.Atoi(strings.TrimPrefix(collectionName, prefix))
	if err != nil || dimension <= 0 {
		return 0, false
	}
	return dimension, true
}

func matchesDimensionCollection(collectionName, baseName string) bool {
	_, ok := collectionDimensionForBase(collectionName, baseName)
	return ok
}
