package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fiftyapi/internal/mailer"
	"fiftyapi/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

func validPublicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http"
}
func (a *API) invitationConfig(w http.ResponseWriter, r *http.Request, id identity) {
	sender, _, err := a.invitationSender(r.Context())
	if err != nil {
		writeError(w, 500, "邮件配置不可用，请检查系统设置")
		return
	}
	writeJSON(w, 200, map[string]bool{"configured": sender != nil})
}
func (a *API) inviteUser(w http.ResponseWriter, r *http.Request, id identity) {
	sender, publicURL, mailErr := a.invitationSender(r.Context())
	if mailErr != nil {
		writeError(w, 503, "邮件配置不可用，请检查系统设置")
		return
	}
	if sender == nil {
		writeError(w, 503, "尚未配置邀请邮件，请在系统设置中配置并启用")
		return
	}
	var in struct {
		Email  string `json:"email"`
		Name   string `json:"display_name"`
		Limit5 int64  `json:"limit_5h_microusd"`
		Limit7 int64  `json:"limit_7d_microusd"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Name = strings.TrimSpace(in.Name)
	email, err := mail.ParseAddress(in.Email)
	if err != nil || email.Address != in.Email || len(in.Email) > 254 || strings.ContainsAny(in.Email, "\r\n") || utf8.RuneCountInString(in.Name) > 80 || strings.ContainsAny(in.Name, "\r\n") || in.Limit5 < 0 || in.Limit7 < 0 || in.Limit5 > 1000000000000000 || in.Limit7 > 1000000000000000 {
		writeError(w, 400, "邮箱、显示名称或额度格式无效")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, 500, "无法生成邀请")
		return
	}
	hash := sha256.Sum256([]byte(token))
	user, err := a.store.CreateInvitedUser(r.Context(), id.user.ID, in.Email, in.Name, in.Limit5, in.Limit7, hash[:], time.Now())
	if err != nil {
		writeError(w, 409, "无法创建用户，请检查邮箱或账号是否已存在")
		return
	}
	a.deliverInvitation(w, r, sender, user, hash[:], mailer.Message{Email: in.Email, Name: in.Name, URL: strings.TrimRight(publicURL, "/") + "/#invite=" + token}, 201)
}
func (a *API) resendInvitation(w http.ResponseWriter, r *http.Request, id identity) {
	sender, publicURL, mailErr := a.invitationSender(r.Context())
	if mailErr != nil {
		writeError(w, 503, "邮件配置不可用，请检查系统设置")
		return
	}
	if sender == nil {
		writeError(w, 503, "尚未配置邀请邮件，请在系统设置中配置并启用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, 500, "无法生成邀请")
		return
	}
	hash := sha256.Sum256([]byte(token))
	invitation, err := a.store.RenewInvitation(r.Context(), id.user.ID, target, hash[:], time.Now())
	if err != nil {
		status := 400
		if errors.Is(err, store.ErrInviteCooldown) {
			status = 429
		}
		writeError(w, status, err.Error())
		return
	}
	a.deliverInvitation(w, r, sender, target, hash[:], mailer.Message{Email: invitation.Email, Name: invitation.DisplayName, URL: strings.TrimRight(publicURL, "/") + "/#invite=" + token}, 200)
}
func (a *API) deliverInvitation(w http.ResponseWriter, r *http.Request, sender mailer.Sender, user int64, hash []byte, m mailer.Message, status int) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	sendErr := sender.SendInvitation(ctx, m)
	save, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer stop()
	err := a.store.InvitationDelivery(save, user, hash, sendErr == nil)
	state, warning := "sent", ""
	if sendErr != nil {
		state = "failed"
		warning = "账号已创建，但邮件未确认发送；请检查 SMTP 配置后重新发送邀请"
	}
	if err != nil {
		warning = "邮件发送状态保存失败，请检查账号状态；重新发送会使旧链接失效"
	}
	writeJSON(w, status, map[string]any{"id": user, "mail_state": state, "warning": warning})
}
func (a *API) checkInvitation(w http.ResponseWriter, r *http.Request) {
	if !a.allowAttempt(r, "invitation-check", 30) {
		writeError(w, 429, "请求过于频繁，请稍后重试")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Token) != 43 {
		writeError(w, 400, store.ErrInvitation.Error())
		return
	}
	hash := sha256.Sum256([]byte(in.Token))
	invitation, err := a.store.Invitation(r.Context(), hash[:], time.Now())
	if err != nil {
		writeError(w, 400, store.ErrInvitation.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"email": invitation.Email, "display_name": invitation.DisplayName, "expires_at": invitation.ExpiresAt})
}
func (a *API) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if !a.allowAttempt(r, "invitation-accept", 10) {
		writeError(w, 429, "请求过于频繁，请稍后重试")
		return
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Token) != 43 {
		writeError(w, 400, store.ErrInvitation.Error())
		return
	}
	if !validPassword(in.Password, "user") {
		writeError(w, 400, "密码需为 8–72 字节")
		return
	}
	hash := sha256.Sum256([]byte(in.Token))
	if _, err := a.store.Invitation(r.Context(), hash[:], time.Now()); err != nil {
		writeError(w, 400, store.ErrInvitation.Error())
		return
	}
	password, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	if err = a.store.AcceptInvitation(r.Context(), hash[:], string(password), time.Now()); err != nil {
		writeError(w, 400, store.ErrInvitation.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
