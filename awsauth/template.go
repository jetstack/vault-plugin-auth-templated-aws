package awsauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	texttemplate "text/template"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/vault/logical"
)

type Values struct {
	InstanceHash string
	FQDN         string
	InternalIPv4 string
	BasePath     string
	OutputPath   string
}

func renderTemplates(ctx context.Context, b *backend, req *logical.Request, instance *ec2.Instance, roleName string, role *awsRoleEntry) ([]string, error) {
	values := Values{
		BasePath: role.BasePath,
	}

	if instance == nil {
		return nil, fmt.Errorf("instance cannot be nil")
	}

	if instance.InstanceId != nil {
		values.InstanceHash = *instance.InstanceId
	}

	if instance.PrivateDnsName != nil {
		values.FQDN = *instance.PrivateDnsName
	}

	if instance.PrivateIpAddress != nil {
		values.InternalIPv4 = *instance.PrivateIpAddress
	}

	policies := []string{}

	templates, err := req.Storage.List(ctx, fmt.Sprintf("template/%s/", roleName))
	if err != nil {
		return nil, err
	}

	b.Logger().Info(fmt.Sprintf("templates: %v", templates))
	for _, templateName := range templates {
		template, err := b.lockedTemplateEntry(ctx, req.Storage, roleName, templateName)
		if err != nil {
			return nil, err
		}

		tmpl, err := texttemplate.New("tmpl").Parse(template.Template)
		if err != nil {
			return nil, err
		}

		var buf bytes.Buffer
		err = tmpl.Execute(&buf, values)
		if err != nil {
			return nil, err
		}

		switch template.Type {
		case "policy":
			values.OutputPath = ""

			fullPolicyName := fmt.Sprintf("/sys/policy/%s-%s", templateName, values.InstanceHash)

			b.Logger().Info(fmt.Sprintf("creating policy: '%s' %s", fullPolicyName, buf.String()))
			policies = append(policies, fullPolicyName)
			_, err = b.vaultClient.Logical().Write(fullPolicyName,
				map[string]interface{}{
					"policy": buf.String(),
				},
			)
			if err != nil {
				return nil, err
			}
		case "generic":
			values.OutputPath = template.Path

			m := map[string]interface{}{}
			err := json.Unmarshal(buf.Bytes(), &m)
			if err != nil {
				return nil, err
			}

			fullSecretName := filepath.Join(values.BasePath, values.OutputPath, fmt.Sprintf("%s-%s", template.TemplateName, values.InstanceHash))
			b.Logger().Info(fmt.Sprintf("creating secret: '%s' %v", fullSecretName, m))
			_, err = b.vaultClient.Logical().Write(fullSecretName, m)

			if err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("not a supported template type: %s", template.Type)
		}

	}
	return policies, nil
}
