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
		data          map[string][]byte
		config        map[string]*Credential
		expectedError error
	}{
		{
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
			data: map[string][]byte{
				"10.20.30.40.usernam":  []byte(testUsername),
				"10.20.30.40.password": []byte(testPassword),
			},
			config:        nil,
			expectedError: ErrUnknownSecretKey,
		},
		{
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
			data: map[string][]byte{
				"10.20.30.40.username": []byte(testPassword),
			},
			config: map[string]*Credential{
				testIP: &Credential{
					Password: testPassword,
				},
			},
			expectedError: ErrCredentialMissing,
		},
		{
			data: map[string][]byte{
				"10.20.30.40": []byte(testUsername),
			},
			config:        nil,
			expectedError: ErrUnknownSecretKey,
		},
	}

	resultConfig := make(map[string]*Credential)
	cleanUPResultConfig := func(config map[string]*Credential) {
		for k := range config {
			delete(config, k)
		}
	}

	for _, testcase := range testcases {
		err := parseConfig(testcase.data, resultConfig)
		if err != testcase.expectedError {
			t.Fatalf("Parsing Secret failed for data %+v: %s", testcase.data, err)
		}
		if testcase.config != nil && !reflect.DeepEqual(testcase.config, resultConfig) {
			t.Fatalf("Parsing Secret failed for data %+v expected config %+v and actual config %+v",
				testcase.data, resultConfig, testcase.config)
		}
		cleanUPResultConfig(resultConfig)
	}
}
