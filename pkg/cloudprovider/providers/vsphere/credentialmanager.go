package vsphere

import (
	"fmt"
	"github.com/golang/glog"
	"gopkg.in/gcfg.v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/listers/core/v1"
	"net/http"
)

type Credential struct {
	User     string `gcfg:"user"`
	Password string `gcfg:"password"`
}

type CredentialManager interface {
	GetCredential(string) (*Credential, error)
	GetCredentials() (map[string]Credential, error)
}

var _ CredentialManager = &SecretCredentialManager{}

type SecretCredentialManager struct {
	Secret          *corev1.Secret
	SecretName      string
	SecretNamespace string
	SecretLister    v1.SecretLister
	VirtualCenter   map[string]Credential
}

func (secretCredentialManager *SecretCredentialManager) GetCredential(server string) (*Credential, error) {
	err := secretCredentialManager.updateCredentialsMap()
	// Handle secret deletion
	if err != nil {
		statusErr, ok := err.(*apierrors.StatusError)
		if ok && statusErr.ErrStatus.Code != http.StatusNotFound || !ok {
			return nil, err
		}
		glog.Warningf("secret %q not found", secretCredentialManager.SecretName)
	}
	// Cases:
	// 1. Secret Deleted finding credentials from cache
	// 2. Secret Not Added at a first place will return error
	// 3. Secret Added but not for asked vCenter Server
	credentials, found := secretCredentialManager.VirtualCenter[server]
	if !found {
		return &credentials, fmt.Errorf("credentials not found for server %q", server)
	}
	return &credentials, nil
}

func (secretCredentialManager *SecretCredentialManager) GetCredentials() (map[string]Credential, error) {
	err := secretCredentialManager.updateCredentialsMap()
	if err != nil {
		return nil, err
	}
	return secretCredentialManager.VirtualCenter, err
}

func (secretCredentialManager *SecretCredentialManager) updateCredentialsMap() error {
	secret, err := secretCredentialManager.SecretLister.Secrets(secretCredentialManager.SecretNamespace).Get(secretCredentialManager.SecretName)
	if err != nil {
		return err
	}
	if secretCredentialManager.Secret != nil ||
		secretCredentialManager.Secret.GetResourceVersion() == secret.GetResourceVersion() {
		return nil
	}
	secretCredentialManager.Secret = secret
	return secretCredentialManager.parseSecret()
}

func (secretCredentialManager *SecretCredentialManager) parseSecret() error {
	confData, found := secretCredentialManager.Secret.Data["vsphere.conf"]
	if !found {
		return fmt.Errorf("Cannot find vsphere.conf in secret %q which is namespace %q ",
			secretCredentialManager.Secret, secretCredentialManager.SecretNamespace)
	}
	return gcfg.ReadStringInto(secretCredentialManager.VirtualCenter, string(confData))
}
