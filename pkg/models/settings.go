package models

type PluginSettings struct {
	Url           string                `json:"url"`
	CertInputMode string                `json:"certInputMode"`
	Secrets       *SecretPluginSettings `json:"-"`
}

type SecretPluginSettings struct {
	ClientCertData string `json:"clientCertData"`
	ClientKeyData  string `json:"clientKeyData"`
	CaCertData     string `json:"caCertData"`
	BearerToken    string `json:"bearerToken"`
}
