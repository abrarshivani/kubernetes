package vsphere

import (
	"testing"
	"reflect"
)

func TestParseSecretConfig(t *testing.T) {
	var (
		testUsername = "Admin"
		testPassword = "Password"
		testIP       = "10.20.30.40"
	)
	var testcases = []struct {
		testName 	  string
		data          map[string][]byte
		config        map[string]*Credential
		expectedError error
	}{
		{
			testName: "Valid username and password",
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testUsername),
				"10.20.30.40.password": []byte(testPassword),
			},
			config: map[string]*Credential{
				testIP: &Credential{
					User:     testUsername,
					Password: testPassword,
				},
			},
			expectedError: nil,
		},
		{
			testName: "Invalid username key with valid password key",
			data: map[string][]byte{
				"10.20.30.40.usernam":  []byte(testUsername),
				"10.20.30.40.password": []byte(testPassword),
			},
			config:        nil,
			expectedError: ErrUnknownSecretKey,
		},
		{
			testName: "Missing username",
			data: map[string][]byte{
				"10.20.30.40.password": []byte(testPassword),
			},
			config: map[string]*Credential{
				testIP: &Credential{
					Password: testPassword,
				},
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "Missing password",
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testUsername),
			},
			config: map[string]*Credential{
				testIP: &Credential{
					User: testUsername,
				},
			},
			expectedError: ErrCredentialMissing,
		},
		{
			testName: "IP with unknown key",
			data: map[string][]byte{
				"10.20.30.40": []byte(testUsername),
			},
			config:        nil,
			expectedError: ErrUnknownSecretKey,
		},
	}

	resultConfig := make(map[string]*Credential)
	cleanupResultConfig := func(config map[string]*Credential) {
		for k := range config {
			delete(config, k)
		}
	}

	for _, testcase := range testcases {
		err := parseConfig(testcase.data, resultConfig)
		t.Logf("Executing Testcase: %s", testcase.testName)
		if err != testcase.expectedError {
			t.Fatalf("Parsing Secret failed for data %+v: %s", testcase.data, err)
		}
		if testcase.config != nil && !reflect.DeepEqual(testcase.config, resultConfig) {
			t.Fatalf("Parsing Secret failed for data %+v expected config %+v and actual config %+v",
				testcase.data, resultConfig, testcase.config)
		}
		cleanupResultConfig(resultConfig)
	}
}
