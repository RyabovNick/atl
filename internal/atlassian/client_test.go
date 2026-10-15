package atlassian

import "testing"

func TestBadRequestMessage(t *testing.T) {
	for body, want := range map[string]string{
		`{"errorMessages":["a"],"errors":{"z":"2","b":"1"}}`: "a; b: 1; z: 2",
		`{"statusCode":400,"message":"Could not parse cql"}`: "Could not parse cql",
		`{}`:       "",
		`not json`: "",
	} {
		if got := badRequestMessage([]byte(body)); got != want {
			t.Errorf("%s: got %q, want %q", body, got, want)
		}
	}
}
