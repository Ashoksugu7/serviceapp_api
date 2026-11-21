package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"serviceops360/api/internal/service"
)

// WorkflowRule ties an automatically-applied status to a staff role and,
// optionally, to one value from the profile form.
type WorkflowRule struct {
	StatusID, StaffRoleID, ConditionFieldKey string
	ConditionValue                           any
	Priority                                 int
}

func (s *CatalogStore) ListWorkflowRules(ctx context.Context, companyID, profileID string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT jsonb_build_object('id',w.id,'company_id',w.company_id,'profile_id',w.profile_id,'status_id',w.status_id,'status_name',st.name,'staff_role_id',w.staff_role_id,'staff_role_name',sr.name,'condition_field_key',w.condition_field_key,'condition_value',w.condition_value,'priority',w.priority) FROM service_profile_workflow_rules w JOIN service_profile_statuses st ON st.company_id=w.company_id AND st.id=w.status_id JOIN staff_roles sr ON sr.company_id=w.company_id AND sr.id=w.staff_role_id WHERE w.company_id=$1 AND w.profile_id=$2 ORDER BY w.priority,w.id`, companyID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		item, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CatalogStore) SetWorkflowRule(ctx context.Context, companyID, profileID, statusID string, rule WorkflowRule) (map[string]any, error) {
	if rule.StaffRoleID == "" {
		return nil, service.FieldErrors{"staff_role_id": "is required"}
	}
	if (rule.ConditionFieldKey == "") != (rule.ConditionValue == nil) {
		return nil, service.FieldErrors{"condition_field_key": "choose both a field and a value, or neither"}
	}
	if rule.Priority < 0 {
		return nil, service.FieldErrors{"priority": "must be zero or greater"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_profile_statuses WHERE company_id=$1 AND profile_id=$2 AND id=$3 AND enabled)`, companyID, profileID, statusID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_roles WHERE company_id=$1 AND id=$2)`, companyID, rule.StaffRoleID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, service.FieldErrors{"staff_role_id": "must be a role in this company"}
	}
	if rule.ConditionFieldKey != "" {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2 AND field_key=$3 AND enabled AND field_type<>'staff_role')`, companyID, profileID, rule.ConditionFieldKey).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, service.FieldErrors{"condition_field_key": "must be an active non-staff field in this profile"}
		}
	}
	conditionJSON, err := json.Marshal(rule.ConditionValue)
	if err != nil {
		return nil, service.FieldErrors{"condition_value": "must be a JSON value"}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO service_profile_workflow_rules(company_id,profile_id,status_id,staff_role_id,condition_field_key,condition_value,priority) VALUES($1,$2,$3,$4,NULLIF($5,''),CASE WHEN $5='' THEN NULL ELSE $6::jsonb END,$7) ON CONFLICT(company_id,profile_id,status_id) DO UPDATE SET staff_role_id=EXCLUDED.staff_role_id,condition_field_key=EXCLUDED.condition_field_key,condition_value=EXCLUDED.condition_value,priority=EXCLUDED.priority,updated_at=now()`, companyID, profileID, statusID, rule.StaffRoleID, rule.ConditionFieldKey, conditionJSON, rule.Priority); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	items, err := s.ListWorkflowRules(ctx, companyID, profileID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item["status_id"] == statusID {
			return item, nil
		}
	}
	return nil, ErrNotFound
}

func (s *CatalogStore) DeleteWorkflowRule(ctx context.Context, companyID, profileID, statusID string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM service_profile_workflow_rules WHERE company_id=$1 AND profile_id=$2 AND status_id=$3`, companyID, profileID, statusID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// workflowStatus resolves the highest-priority matching rule. A rule applies
// when a staff member is selected for its role and its optional field value is
// exactly equal to the configured value.
func workflowStatus(ctx context.Context, tx pgx.Tx, companyID, profileID string, formData map[string]any) (string, error) {
	rows, err := tx.Query(ctx, `SELECT w.status_id::text,w.staff_role_id::text,w.condition_field_key,w.condition_value FROM service_profile_workflow_rules w JOIN service_profile_statuses st ON st.company_id=w.company_id AND st.id=w.status_id AND st.enabled WHERE w.company_id=$1 AND w.profile_id=$2 ORDER BY w.priority,w.id`, companyID, profileID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	roles := map[string]bool{}
	roleRows, err := tx.Query(ctx, `SELECT field_key,config->>'role_id' FROM service_profile_fields WHERE company_id=$1 AND profile_id=$2 AND field_type='staff_role' AND enabled`, companyID, profileID)
	if err != nil {
		return "", err
	}
	for roleRows.Next() {
		var key, roleID string
		if err := roleRows.Scan(&key, &roleID); err != nil {
			roleRows.Close()
			return "", err
		}
		if staffID, ok := formData[key].(string); ok && staffID != "" {
			var assigned bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_role_assignments WHERE company_id=$1 AND staff_id=$2 AND role_id=$3)`, companyID, staffID, roleID).Scan(&assigned); err != nil {
				roleRows.Close()
				return "", err
			}
			if assigned {
				roles[roleID] = true
			}
		}
	}
	if err := roleRows.Err(); err != nil {
		roleRows.Close()
		return "", err
	}
	roleRows.Close()
	for rows.Next() {
		var statusID, roleID string
		var conditionKey *string
		var conditionRaw []byte
		if err := rows.Scan(&statusID, &roleID, &conditionKey, &conditionRaw); err != nil {
			return "", err
		}
		if !roles[roleID] {
			continue
		}
		if conditionKey != nil {
			var expected any
			if err := json.Unmarshal(conditionRaw, &expected); err != nil {
				return "", err
			}
			if actual, ok := formData[*conditionKey]; !ok || !jsonEqual(actual, expected) {
				continue
			}
		}
		return statusID, nil
	}
	return "", rows.Err()
}

func workflowRuleMatchesStatus(ctx context.Context, tx pgx.Tx, companyID, profileID, statusID string, formData map[string]any) (bool, error) {
	var mapped bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_profile_workflow_rules WHERE company_id=$1 AND profile_id=$2 AND status_id=$3)`, companyID, profileID, statusID).Scan(&mapped); err != nil {
		return false, err
	}
	if !mapped {
		return true, nil
	}
	matched, err := workflowStatus(ctx, tx, companyID, profileID, formData)
	return matched == statusID, err
}

func workflowRuleError(statusID string) error {
	return service.InvalidState(fmt.Sprintf("The staff-role and condition requirements for this workflow status are not met (%s).", statusID))
}
