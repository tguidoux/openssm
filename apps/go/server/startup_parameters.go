package main

import (
	"context"

	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// seedKMSKeys creates configured KMS keys whose alias does not exist yet.
func seedKMSKeys(svc *kms.Service, keys []StartupKey, log logger.LoggerInterface) {
	for _, k := range keys {
		if _, err := svc.ResolveKeyID(k.Alias); err == nil {
			continue
		}
		if _, err := svc.CreateKey(k.Description, k.Alias); err != nil {
			log.Fatalf("failed to create startup KMS key %q: %v", k.Alias, err)
			return
		}
		log.Infof("created startup KMS key %q", k.Alias)
	}
}

// seedParameters creates configured parameters. Existing parameters are left
// untouched so restarts with persistent storage don't create new versions.
func seedParameters(svc *parameters.Service, params []StartupParameter, log logger.LoggerInterface) {
	ctx := context.Background()
	for _, p := range params {
		typ := p.Type
		if typ == "" {
			typ = parameters.TypeString
		}
		in := &parameters.PutParameterInput{
			Name:  p.Name,
			Value: &p.Value,
			Type:  typ,
			KeyID: p.KeyID,
			Tier:  p.Tier,
		}
		if p.Description != "" {
			in.Description = &p.Description
		}
		for k, v := range p.Tags {
			in.Tags = append(in.Tags, types.Tag{Key: k, Value: v})
		}
		if _, err := svc.Put(ctx, in); err != nil {
			if types.IsCode(err, types.ErrParameterAlreadyExists) {
				continue
			}
			log.Errorf("failed to create startup parameter %q: %v", p.Name, err)
			continue
		}
		log.Infof("created startup parameter %q", p.Name)
	}
}
