package manager

import (
	"encoding/json"
	"os"
)

// TODO delete this file

type testData struct {
	Project  string `json:"project"`
	MD5      string `json:"md5"`
	Content  []byte `json:"content"`
	ExpParam string `json:"expParam"`
}

func getTestData(file string) *testData {
	content, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}

	var payload testData
	err = json.Unmarshal(content, &payload)
	if err != nil {
		panic(err)
	}
	return &payload
}
