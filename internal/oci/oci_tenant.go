package oci

import (
	"fmt"
	"oci-help/internal/config"
	"oci-help/internal/proxy"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/identitydomains"
	"github.com/oracle/oci-go-sdk/v65/ospgateway"
)

// sdkTimeString 把 SDKTime 格式化成本地时间字符串，空值返回空串。
func sdkTimeString(t *common.SDKTime) string {
	if t == nil || t.Time.IsZero() {
		return ""
	}
	return t.Time.Format("2006-01-02 15:04:05")
}

// TenantUser 是租户下的一个用户。
type TenantUser struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	LifecycleState string `json:"lifecycleState"`
	IsMfaActivated bool   `json:"isMfaActivated"`
	TimeCreated    string `json:"timeCreated"`
	LastLogin      string `json:"lastLogin"`
}

// TenantInfo 是租户（账号）概览信息。
type TenantInfo struct {
	Name        string       `json:"name"`
	ID          string       `json:"id"`
	Description string       `json:"description"`
	HomeRegion  string       `json:"homeRegion"`
	PlanType    string       `json:"planType"`
	Regions     []string     `json:"regions"`
	Users       []TenantUser `json:"users"`
}

// ApiKeyView 是一条 API 密钥。
type ApiKeyView struct {
	Fingerprint    string `json:"fingerprint"`
	KeyID          string `json:"keyId"`
	LifecycleState string `json:"lifecycleState"`
	TimeCreated    string `json:"timeCreated"`
}

// MfaDeviceView 是一台 MFA 设备。
type MfaDeviceView struct {
	ID             string `json:"id"`
	IsActivated    bool   `json:"isActivated"`
	LifecycleState string `json:"lifecycleState"`
	TimeCreated    string `json:"timeCreated"`
}

// GetTenantInfo 汇总租户名称、区域列表与用户列表。
func (ac *AccountCtx) GetTenantInfo() (TenantInfo, error) {
	var info TenantInfo
	resp, err := ac.identityClient.GetTenancy(Ctx, identity.GetTenancyRequest{
		TenancyId:       common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return info, err
	}
	info.Name = S(resp.Name)
	info.ID = S(resp.Id)
	info.Description = S(resp.Description)
	info.HomeRegion = S(resp.HomeRegionKey)

	// 区域订阅
	if regions, err := ac.identityClient.ListRegionSubscriptions(Ctx, identity.ListRegionSubscriptionsRequest{
		TenancyId:       common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}); err == nil {
		for _, r := range regions.Items {
			info.Regions = append(info.Regions, S(r.RegionName))
		}
	}

	// 套餐类型（尽力而为）
	info.PlanType = ac.planType()

	// 用户列表
	users, err := ac.ListUsers()
	if err == nil {
		info.Users = users
	}
	return info, nil
}

// planType 通过 OSP 订阅信息推断账号套餐类型，失败返回空串。
func (ac *AccountCtx) planType() string {
	resp, err := ac.ospClient.ListSubscriptions(Ctx, ospgateway.ListSubscriptionsRequest{
		OspHomeRegion:   common.String(ac.Oracle.Region),
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil || len(resp.Items) == 0 {
		return ""
	}
	return string(resp.Items[0].PlanType)
}

// ListUsers 列出租户下的全部用户。
func (ac *AccountCtx) ListUsers() ([]TenantUser, error) {
	var out []TenantUser
	var page *string
	for {
		resp, err := ac.identityClient.ListUsers(Ctx, identity.ListUsersRequest{
			CompartmentId:   common.String(ac.Oracle.Tenancy),
			Page:            page,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err != nil {
			return out, err
		}
		for _, u := range resp.Items {
			out = append(out, TenantUser{
				ID:             S(u.Id),
				Name:           S(u.Name),
				Email:          S(u.Email),
				LifecycleState: string(u.LifecycleState),
				IsMfaActivated: BVal(u.IsMfaActivated),
				TimeCreated:    sdkTimeString(u.TimeCreated),
				LastLogin:      sdkTimeString(u.LastSuccessfulLoginTime),
			})
		}
		if resp.OpcNextPage == nil || len(resp.Items) == 0 {
			break
		}
		page = resp.OpcNextPage
	}
	return out, nil
}

// ListApiKeys 列出用户的 API 密钥。
func (ac *AccountCtx) ListApiKeys(userID string) ([]ApiKeyView, error) {
	resp, err := ac.identityClient.ListApiKeys(Ctx, identity.ListApiKeysRequest{
		UserId:          &userID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	var out []ApiKeyView
	for _, k := range resp.Items {
		out = append(out, ApiKeyView{
			Fingerprint:    S(k.Fingerprint),
			KeyID:          S(k.KeyId),
			LifecycleState: string(k.LifecycleState),
			TimeCreated:    sdkTimeString(k.TimeCreated),
		})
	}
	return out, nil
}

// DeleteApiKey 删除用户指定指纹的 API 密钥。
func (ac *AccountCtx) DeleteApiKey(userID, fingerprint string) error {
	_, err := ac.identityClient.DeleteApiKey(Ctx, identity.DeleteApiKeyRequest{
		UserId:          &userID,
		Fingerprint:     &fingerprint,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return err
}

// ListMfaDevices 列出用户的 MFA（TOTP）设备。
func (ac *AccountCtx) ListMfaDevices(userID string) ([]MfaDeviceView, error) {
	resp, err := ac.identityClient.ListMfaTotpDevices(Ctx, identity.ListMfaTotpDevicesRequest{
		UserId:          &userID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	var out []MfaDeviceView
	for _, d := range resp.Items {
		out = append(out, MfaDeviceView{
			ID:             S(d.Id),
			IsActivated:    BVal(d.IsActivated),
			LifecycleState: string(d.LifecycleState),
			TimeCreated:    sdkTimeString(d.TimeCreated),
		})
	}
	return out, nil
}

// DeleteMfaDevice 删除用户的一台 MFA 设备。
func (ac *AccountCtx) DeleteMfaDevice(userID, deviceID string) error {
	_, err := ac.identityClient.DeleteMfaTotpDevice(Ctx, identity.DeleteMfaTotpDeviceRequest{
		UserId:          &userID,
		MfaTotpDeviceId: &deviceID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return err
}

// DeleteUser 删除用户。
func (ac *AccountCtx) DeleteUser(userID string) error {
	_, err := ac.identityClient.DeleteUser(Ctx, identity.DeleteUserRequest{
		UserId:          &userID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return err
}

// UpdateUserInfo 更新用户的邮箱与描述。
func (ac *AccountCtx) UpdateUserInfo(userID, email, description string) error {
	details := identity.UpdateUserDetails{}
	if email != "" {
		details.Email = common.String(email)
	}
	if description != "" {
		details.Description = common.String(description)
	}
	_, err := ac.identityClient.UpdateUser(Ctx, identity.UpdateUserRequest{
		UserId:            &userID,
		UpdateUserDetails: details,
		RequestMetadata:   getCustomRequestMetadataWithRetryPolicy(),
	})
	return err
}

// ResetUserPassword 重置用户的控制台登录密码，返回新密码。
// 先尝试经典 IAM API（适用于传统用户）；失败则回退到 Identity Domains 的 SCIM 接口。
func (ac *AccountCtx) ResetUserPassword(userID string) (string, error) {
	resp, err := ac.identityClient.CreateOrResetUIPassword(Ctx, identity.CreateOrResetUIPasswordRequest{
		UserId:          &userID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err == nil && resp.UiPassword.Password != nil {
		return *resp.UiPassword.Password, nil
	}
	classicErr := err

	url, derr := ac.domainURL()
	if derr != nil || url == "" {
		return "", fmt.Errorf("重置密码失败：经典 API 不可用（%v），且未找到可用的 Identity Domain", classicErr)
	}
	dc, cerr := identitydomains.NewIdentityDomainsClientWithConfigurationProvider(ac.provider, url)
	if cerr != nil {
		return "", fmt.Errorf("创建 Identity Domains 客户端失败: %w", cerr)
	}
	tenantProxy := ""
	if ac.Tenant != nil {
		tenantProxy = ac.Tenant.Proxy
	}
	if tenantProxy == "" {
		tenantProxy = config.C.Proxy
	}
	proxy.Apply(&dc.BaseClient, tenantProxy)

	filter := fmt.Sprintf("ocid eq \"%s\"", userID)
	lr, lerr := dc.ListUsers(Ctx, identitydomains.ListUsersRequest{
		Filter:          &filter,
		Attributes:      common.String("id,ocid,userName"),
		Count:           common.Int(1),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if lerr != nil {
		return "", fmt.Errorf("在 Identity Domain 中查询用户失败: %w", lerr)
	}
	if len(lr.Users.Resources) == 0 || lr.Users.Resources[0].Id == nil {
		return "", fmt.Errorf("在 Identity Domain 中未找到用户: %s", userID)
	}
	scimID := *lr.Users.Resources[0].Id
	pw := randomPassword(14)
	_, perr := dc.PutUserPasswordChanger(Ctx, identitydomains.PutUserPasswordChangerRequest{
		UserPasswordChangerId: &scimID,
		UserPasswordChanger: identitydomains.UserPasswordChanger{
			Schemas:            []string{"urn:ietf:params:scim:schemas:oracle:idcs:UserPasswordChanger"},
			Password:           &pw,
			BypassNotification: common.Bool(true),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if perr != nil {
		return "", fmt.Errorf("重置密码失败（可能需要为该 API Key 用户授予 Identity Domain 的「用户管理员」角色）: %w", perr)
	}
	return pw, nil
}

// domainURL 返回租户下第一个处于 ACTIVE 状态的 Identity Domain 的 URL。
func (ac *AccountCtx) domainURL() (string, error) {
	resp, err := ac.identityClient.ListDomains(Ctx, identity.ListDomainsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", err
	}
	for _, d := range resp.Items {
		if d.LifecycleState == identity.DomainLifecycleStateActive && d.Url != nil {
			return *d.Url, nil
		}
	}
	return "", nil
}
