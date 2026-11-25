package config

import environment "github.com/tguidoux/openssm/pkgs/v1/environment"

// LocalAWSSSMEndpointURL is the default OpenSSM endpoint for local development.
const LocalAWSSSMEndpointURL = "http://localhost:9330"

func GetRegion(env environment.Environment) string {
	switch env {
	case environment.LOCAL:
		return ""
	default:
		return "us-east-1"
	}
}

func GetSSMRegion(env environment.Environment) string {
	return GetRegion(env)
}

func GetAWSSSMEndpointURL(env environment.Environment) string {
	switch env {
	case environment.LOCAL:
		return LocalAWSSSMEndpointURL
	default:
		return ""
	}
}
