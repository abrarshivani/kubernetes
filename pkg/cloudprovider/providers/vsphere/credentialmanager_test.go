package vsphere

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func TestSecretCredentialManager_GetCredential(t *testing.T) {
	var (
		userKey      = "username"
		passwordKey  = "password"
		testUser     = "user"
		testPassword = "password"
		testServer   = "0.0.0.0"
	)
	var (
		secretName      = "vsconf"
		secretNamespace = "kube-system"
	)
	var (
		addSecretOp      = "ADD_SECRET_OP"
		getCredentialsOp = "GET_CREDENTIAL_OP"
		deleteSecretOp   = "DELETE_SECRET_OP"
	)
	type GetCredentialsTest struct {
		server   string
		username string
		password string
		err      error
	}
	type OpSecretTest struct {
		secret *corev1.Secret
	}
	type testEnv struct {
		testName       string
		ops            []string
		expectedValues []interface{}
	}

	metaObj := metav1.ObjectMeta{
		Name:      secretName,
		Namespace: secretNamespace,
	}

	defaultSecret := &corev1.Secret{
		ObjectMeta: metaObj,
		Data: map[string][]byte{
			testServer + "." + userKey:     []byte(testUser),
			testServer + "." + passwordKey: []byte(testPassword),
		},
	}

	tests := []testEnv{
		{
			testName: "Deleting secret should give the credentials from cache",
			ops:      []string{addSecretOp, getCredentialsOp, deleteSecretOp, getCredentialsOp},
			expectedValues: []interface{}{
				OpSecretTest{
					secret: defaultSecret,
				},
				GetCredentialsTest{
					username: testUser,
					password: testPassword,
					server:   testServer,
				},
				OpSecretTest{
					secret: defaultSecret,
				},
				GetCredentialsTest{
					username: testUser,
					password: testPassword,
					server:   testServer,
				},
			},
		},

	}

	for _, test := range tests {
		t.Logf("Executing Testcase: %s", test.testName)
	}
}

func TestParseSecretConfig(t *testing.T) {
	var (
		testUsername = "Admin"
		testPassword = "Password"
		testIP       = "10.20.30.40"
	)
	var testcases = []struct {
		testName      string
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
				testIP: {
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
				testIP: {
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
				testIP: {
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
