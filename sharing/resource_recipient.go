package sharing

import (
	"context"
	"log"
	"strings"

	"github.com/databricks/databricks-sdk-go/service/sharing"
	"github.com/databricks/terraform-provider-databricks/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func recepientPropertiesSuppressDiff(k, old, new string, d *schema.ResourceData) bool {
	isPossiblySetAutomatically := k == "properties_kvpairs.0.properties.%" && old == "1" && new == "0"
	isAutoGeneratedName := strings.HasPrefix(k, "properties_kvpairs.0.properties.databricks.") && new == ""
	if isPossiblySetAutomatically || isAutoGeneratedName {
		log.Printf("[DEBUG] Suppressing diff for k=%#v old=%#v new=%#v", k, old, new)
		return true
	}
	return false
}

func ResourceRecipient() common.Resource {
	recipientSchema := common.StructToSchema(sharing.RecipientInfo{}, func(s map[string]*schema.Schema) map[string]*schema.Schema {
		common.CustomizeSchemaPath(s, "authentication_type").SetForceNew().SetRequired().SetValidateFunc(
			validation.StringInSlice([]string{"TOKEN", "DATABRICKS"}, false))
		common.CustomizeSchemaPath(s, "sharing_code").SetSuppressDiff().SetForceNew().SetSensitive()
		common.CustomizeSchemaPath(s, "name").SetForceNew().SetRequired().SetCustomSuppressDiff(common.EqualFoldDiffSuppress)
		common.CustomizeSchemaPath(s, "owner").SetSuppressDiff()
		common.CustomizeSchemaPath(s, "properties_kvpairs").SetSuppressDiff()
		common.CustomizeSchemaPath(s, "properties_kvpairs", "properties").SetCustomSuppressDiff(recepientPropertiesSuppressDiff)
		common.CustomizeSchemaPath(s, "data_recipient_global_metastore_id").SetForceNew().SetConflictsWith([]string{}, []string{"ip_access_list"})
		common.CustomizeSchemaPath(s, "ip_access_list").SetConflictsWith([]string{}, []string{"data_recipient_global_metastore_id"})

		// ReadOnly fields
		for _, path := range []string{"created_at", "created_by", "updated_at", "updated_by", "metastore_id", "region",
			"cloud", "activated", "activation_url"} {
			common.CustomizeSchemaPath(s, path).SetReadOnly()
		}
		common.CustomizeSchemaPath(s, "tokens").SetReadOnly().SetOptional()
		for _, path := range []string{"id", "created_at", "created_by", "activation_url", "expiration_time", "updated_at", "updated_by"} {
			common.CustomizeSchemaPath(s, "tokens", path).SetReadOnly()
		}

		return s
	})
	return common.Resource{
		Schema: recipientSchema,
		Create: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			w, err := c.WorkspaceClient()
			if err != nil {
				return err
			}
			var createRecipientRequest sharing.CreateRecipient
			common.DataToStructPointer(d, recipientSchema, &createRecipientRequest)
			ri, err := w.Recipients.Create(ctx, createRecipientRequest)
			if err != nil {
				return err
			}
			d.SetId(ri.Name)
			return nil
		},
		Read: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			w, err := c.WorkspaceClient()
			if err != nil {
				return err
			}
			ri, err := w.Recipients.GetByName(ctx, d.Id())
			if err != nil {
				return err
			}
			if ri.PropertiesKvpairs != nil {
				// Remove databricks.* properties from the response as we can't set them
				for k := range ri.PropertiesKvpairs.Properties {
					if strings.HasPrefix(k, "databricks.") {
						delete(ri.PropertiesKvpairs.Properties, k)
					}
				}
			}
			return common.StructToData(ri, recipientSchema, d)
		},
		Update: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			w, err := c.WorkspaceClient()
			if err != nil {
				return err
			}
			var updateRecipientRequest sharing.UpdateRecipient
			common.DataToStructPointer(d, recipientSchema, &updateRecipientRequest)
			updateRecipientRequest.Name = d.Id()

			if d.HasChange("owner") {
				err = w.Recipients.Update(ctx, sharing.UpdateRecipient{
					Name:  updateRecipientRequest.Name,
					Owner: updateRecipientRequest.Owner,
				})
				if err != nil {
					return err
				}
			}

			if !d.HasChangeExcept("owner") {
				return nil
			}

			updateRecipientRequest.Owner = ""
			err = w.Recipients.Update(ctx, updateRecipientRequest)
			if err != nil {
				if d.HasChange("owner") {
					// Rollback
					old, new := d.GetChange("owner")
					rollbackErr := w.Recipients.Update(ctx, sharing.UpdateRecipient{
						Name:  updateRecipientRequest.Name,
						Owner: old.(string),
					})
					if rollbackErr != nil {
						return common.OwnerRollbackError(err, rollbackErr, old.(string), new.(string))
					}
				}
				return err
			}
			return nil
		},
		Delete: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			w, err := c.WorkspaceClient()
			if err != nil {
				return err
			}
			return w.Recipients.DeleteByName(ctx, d.Id())
		},
	}
}
