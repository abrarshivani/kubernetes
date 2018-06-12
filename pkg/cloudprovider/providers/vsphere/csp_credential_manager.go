package vsphere

import (
	cspvsphere "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
)

type CSPSecretCredentialManager struct {
	*SecretCredentialManager
}

var _ cspvsphere.CredentialStore = &CSPSecretCredentialManager{}

func (secretCredentialManager *CSPSecretCredentialManager) GetCredential(server string) (*cspvsphere.Credential, error) {
	credentenal, err := secretCredentialManager.SecretCredentialManager.GetCredential(server)
	if err != nil {
		return nil, err
	}
	cspCredentials := &cspvsphere.Credential{
		User:     credentenal.User,
		Password: credentenal.Password,
	}
	return cspCredentials, nil
}
