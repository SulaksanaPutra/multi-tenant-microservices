package domain

const (
	PermissionPaymentsRead    = "payments:read"
	PermissionPaymentsCreate  = "payments:create"
	PermissionPaymentsRefund  = "payments:refund"
	PermissionPaymentsWebhook = "payments:webhook"
	PermissionPaymentsManage  = "payments:manage"
)

func DomainPermissions() []string {
	return []string{
		PermissionPaymentsRead,
		PermissionPaymentsCreate,
		PermissionPaymentsRefund,
		PermissionPaymentsWebhook,
		PermissionPaymentsManage,
	}
}

