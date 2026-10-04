package repository

import "context"

// Dashboard returns the small, already-aggregated data set needed by the web
// overview. Keeping these counts server-side avoids loading entire collections
// in the browser merely to calculate dashboard totals.
func (s *CatalogStore) Dashboard(ctx context.Context, companyID string) (map[string]any, error) {
	if companyID == "" {
		return scanObject(s.pool.QueryRow(ctx, `
			SELECT jsonb_build_object(
				'scope', 'platform',
				'companies', jsonb_build_object(
					'total', (SELECT count(*) FROM companies),
					'active', (SELECT count(*) FROM companies WHERE status = 'ACTIVE'),
					'suspended', (SELECT count(*) FROM companies WHERE status = 'SUSPENDED')
				)
			)`))
	}

	return scanObject(s.pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'scope', 'company',
			'metrics', jsonb_build_object(
				'service_records', (SELECT count(*) FROM service_requests WHERE company_id = $1),
				'open_records', (SELECT count(*) FROM service_requests r JOIN service_profile_statuses st ON st.company_id = r.company_id AND st.id = r.status_id WHERE r.company_id = $1 AND NOT st.is_closed),
				'closed_records', (SELECT count(*) FROM service_requests r JOIN service_profile_statuses st ON st.company_id = r.company_id AND st.id = r.status_id WHERE r.company_id = $1 AND st.is_closed),
				'customers', (SELECT count(*) FROM customers WHERE company_id = $1),
				'out_store_sent', (SELECT count(*) FROM out_store_entries WHERE company_id = $1 AND status = 'SENT'),
				'standby_available', (SELECT count(*) FROM standby_items WHERE company_id = $1 AND status = 'AVAILABLE'),
				'standby_issued', (SELECT count(*) FROM standby_items WHERE company_id = $1 AND status = 'ISSUED')
			),
			'features', jsonb_build_object(
				'out_store_enabled', EXISTS(SELECT 1 FROM service_profiles WHERE company_id = $1 AND is_active AND out_store_enabled)
			),
			'records_by_status', COALESCE((
				SELECT jsonb_agg(jsonb_build_object('status_id', status_id, 'status_name', status_name, 'count', count) ORDER BY status_name)
				FROM (
					SELECT st.id::text AS status_id, st.name AS status_name, count(*)
					FROM service_requests r JOIN service_profile_statuses st ON st.company_id = r.company_id AND st.id = r.status_id
					WHERE r.company_id = $1 GROUP BY st.id, st.name
				) grouped
			), '[]'::jsonb),
			'recent_records', COALESCE((
				SELECT jsonb_agg(record ORDER BY updated_at DESC)
				FROM (
					SELECT jsonb_build_object('id', r.id, 'request_no', r.request_no, 'service_date', r.service_date, 'updated_at', r.updated_at, 'customer_name', c.name, 'customer_contact', c.contact, 'profile_name', p.name, 'status_name', st.name, 'closed', st.is_closed) AS record, r.updated_at
					FROM service_requests r
					JOIN customers c ON c.company_id = r.company_id AND c.id = r.customer_id
					JOIN service_profiles p ON p.company_id = r.company_id AND p.id = r.profile_id
					JOIN service_profile_statuses st ON st.company_id = r.company_id AND st.id = r.status_id
					WHERE r.company_id = $1 ORDER BY r.updated_at DESC, r.id DESC LIMIT 5
				) recent
			), '[]'::jsonb)
		)`, companyID))
}
