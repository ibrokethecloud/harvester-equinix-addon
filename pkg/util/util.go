package util

import (
	"math/rand"
	"strings"
	"time"
)

func ContainsFinalizer(arr []string, key string) bool {
	for _, v := range arr {
		if v == key {
			return true
		}
	}

	return false
}

func RemoveFinalizer(arr []string, key string) (out []string, modified bool) {
	for _, v := range arr {
		if v != key {
			out = append(out, v)
			modified = true
		}
	}

	return out, modified
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandStringRunes(n int) string {
	rand.Seed(time.Now().UnixNano())
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func LowerRandStringRunes(n int) string {
	s := RandStringRunes(n)
	return strings.ToLower(s)
}
