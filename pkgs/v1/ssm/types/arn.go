package types

import (
	"fmt"
	"strings"
)

// Account holds the account/region identity used to build ARNs.
type Account struct {
	AccountID string
	Region    string
	Partition string
}

func (a Account) partition() string {
	if a.Partition == "" {
		return "aws"
	}
	return a.Partition
}

// ParameterARN returns the ARN of a parameter. Names starting with "/"
// produce "parameter/a/b"; other names produce "parameter/name".
func (a Account) ParameterARN(name string) string {
	return fmt.Sprintf("arn:%s:ssm:%s:%s:parameter/%s", a.partition(), a.Region, a.AccountID, strings.TrimPrefix(name, "/"))
}

// DocumentARN returns the ARN of a document.
func (a Account) DocumentARN(name string) string {
	return fmt.Sprintf("arn:%s:ssm:%s:%s:document/%s", a.partition(), a.Region, a.AccountID, name)
}

// ManagedInstanceARN returns the ARN of a managed instance.
func (a Account) ManagedInstanceARN(id string) string {
	return fmt.Sprintf("arn:%s:ssm:%s:%s:managed-instance/%s", a.partition(), a.Region, a.AccountID, id)
}

// KeyARN returns the ARN of a KMS key.
func (a Account) KeyARN(keyID string) string {
	return fmt.Sprintf("arn:%s:kms:%s:%s:key/%s", a.partition(), a.Region, a.AccountID, keyID)
}

// AliasARN returns the ARN of a KMS alias ("alias/..." expected).
func (a Account) AliasARN(alias string) string {
	return fmt.Sprintf("arn:%s:kms:%s:%s:%s", a.partition(), a.Region, a.AccountID, alias)
}

// UserARN returns a synthetic IAM user ARN for LastModifiedUser fields.
func (a Account) UserARN(user string) string {
	if user == "" {
		user = "openssm"
	}
	return fmt.Sprintf("arn:%s:iam::%s:user/%s", a.partition(), a.AccountID, user)
}
