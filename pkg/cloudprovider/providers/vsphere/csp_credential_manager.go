package vsphere

import (
	cspvsphere "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
)
type CSPSecretCredentialManager struct {
	*SecretCredentialManager
}

func (secretCredentialManager *CSPSecretCredentialManager) GetCredentials(server string) (*cspvsphere.Credential, error) {
	credentenal, err := secretCredentialManager.SecretCredentialManager.GetCredential(server)
	if err != nil {
		return nil, err
	}
	csp_credentials := &cspvsphere.Credential{
		User: credentenal.User,
		Password: credentenal.Password,
	}
	return csp_credentials, nil
}
