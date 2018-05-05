package vsphere

import (
	"errors"
	"fmt"
	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/listers/core/v1"
	"net/http"
	"strings"
	"sync"
)

// Error Messages
const (
	CredentialsNotFoundErrMsg = "Credentials not found"
	CredentialMissingErrMsg = "Username/Password is missing"
	UnknownSecretKeyErrMsg  = "Unknown secret key"
)

// Error constants
var (
	ErrCredentialsNotFound = errors.New(CredentialsNotFoundErrMsg)
	ErrCredentialMissing = errors.New(CredentialMissingErrMsg)
	ErrUnknownSecretKey  = errors.New(UnknownSecretKeyErrMsg)
)

type SecretCache struct {
	cacheLock     sync.Mutex
	VirtualCenter map[string]*Credential
	Secret        *corev1.Secret
}

type Credential struct {
	User     string `gcfg:"user"`
	Password string `gcfg:"password"`
}

type SecretCredentialManager struct {
	SecretName      string
	SecretNamespace string
	SecretLister    v1.SecretLister
	Cache           *SecretCache
}

func (secretCredentialManager *SecretCredentialManager) GetCredential(server string) (*Credential, error) {
	err := secretCredentialManager.updateCredentialsMap()
	// Handle secret deletion
	if err != nil {
		statusErr, ok := err.(*apierrors.StatusError)
		if ok && statusErr.ErrStatus.Code != http.StatusNotFound || !ok {
			return nil, err
		}
		glog.Warningf("secret %q not found in namespace %q", secretCredentialManager.SecretName, secretCredentialManager.SecretNamespace)
	}
	// Cases:
	// 1. Secret Deleted finding credentials from cache
	// 2. Secret Not Added at a first place will return error
	// 3. Secret Added but not for asked vCenter Server
	credential, found := secretCredentialManager.Cache.GetCredential(server)
	if !found {
		glog.Errorf("credentials not found for server %q", server)
		return nil, ErrCredentialsNotFound
	}
	return &credential, nil
}

func (secretCredentialManager *SecretCredentialManager) updateCredentialsMap() error {
	if secretCredentialManager.SecretLister == nil {
		return fmt.Errorf("SecretLister is not initialized")
	}
	secret, err := secretCredentialManager.SecretLister.Secrets(secretCredentialManager.SecretNamespace).Get(secretCredentialManager.SecretName)
	if err != nil {
		glog.Errorf("Cannot get secret %s in namespace %s. error: %q", secretCredentialManager.SecretName, secretCredentialManager.SecretNamespace, err)
		return err
	}
	cacheSecret := secretCredentialManager.Cache.GetSecret()
	if cacheSecret != nil &&
		cacheSecret.GetResourceVersion() == secret.GetResourceVersion() {
		glog.V(4).Infof("VCP SecretCredentialManager: Secret %q will not be updated in cache. Since, secrets have same resource version %q", secretCredentialManager.SecretName, cacheSecret.GetResourceVersion())
		return nil
	}
	secretCredentialManager.Cache.UpdateSecret(secret)
	return secretCredentialManager.Cache.parseSecret()
}

func (cache *SecretCache) GetSecret() *corev1.Secret {
	cache.cacheLock.Lock()
	defer cache.cacheLock.Unlock()
	return cache.Secret
}

func (cache *SecretCache) UpdateSecret(secret *corev1.Secret) {
	cache.cacheLock.Lock()
	defer cache.cacheLock.Unlock()
	cache.Secret = secret
}

func (cache *SecretCache) GetCredential(server string) (Credential, bool) {
	cache.cacheLock.Lock()
	defer cache.cacheLock.Unlock()
	credential, found := cache.VirtualCenter[server]
	if !found {
		return Credential{}, found
	}
	return *credential, found
}

func (cache *SecretCache) parseSecret() error {
	cache.cacheLock.Lock()
	defer cache.cacheLock.Unlock()

	//glog.Errorf("Data %+v, ConfData %+v, String Version %q", cache.Secret.Data["vsphere.conf"], confData, string(confData))
	//return gcfg.ReadStringInto(cache, string(confData))
	return parseConfig(cache.Secret.Data, cache.VirtualCenter)
}

func parseConfig(data map[string][]byte, config map[string]*Credential) error {
	for credentialKey, credentialValue := range data {
		credentialKey = strings.ToLower(credentialKey)
		vcServer := ""
		if strings.HasSuffix(credentialKey, "password") {
			vcServer = strings.Split(credentialKey, ".password")[0]
			if _, ok := config[vcServer]; !ok {
				config[vcServer] = &Credential{}
			}
			config[vcServer].Password = string(credentialValue)
		} else if strings.HasSuffix(credentialKey, "username") {
			vcServer = strings.Split(credentialKey, ".username")[0]
			if _, ok := config[vcServer]; !ok {
				config[vcServer] = &Credential{}
			}
			config[vcServer].User = string(credentialValue)
		} else {
			glog.Errorf("Unknown secret key %s", credentialKey)
			return ErrUnknownSecretKey
		}
	}
	for vcServer, credential := range config {
		if credential.User == "" || credential.Password == "" {
			glog.Errorf("Username/Password is missing for server %s", vcServer)
			return ErrCredentialMissing
		}
	}
	return nil
}
