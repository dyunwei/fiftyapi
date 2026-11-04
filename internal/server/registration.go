package server

import (
	"errors"
	"fiftyapi/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	if !a.allowAttempt(r, "register", 5) {
		writeError(w, 429, "注册过于频繁，请稍后重试")
		return
	}
	initialized, err := a.store.Initialized(r.Context())
	if err != nil {
		writeError(w, 500, "读取初始化状态失败")
		return
	}
	if !initialized {
		writeError(w, 403, store.ErrRegistrationUnavailable.Error())
		return
	}
	var in struct {
		Email    string `json:"email"`
		RealName string `json:"real_name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.RealName = strings.TrimSpace(in.RealName)
	email, err := mail.ParseAddress(in.Email)
	if err != nil || email.Address != in.Email || len(in.Email) > 254 || strings.ContainsAny(in.Email, "\r\n") {
		writeError(w, 400, "请填写有效的邮箱地址")
		return
	}
	if in.RealName == "" || !utf8.ValidString(in.RealName) || utf8.RuneCountInString(in.RealName) > 80 || strings.IndexFunc(in.RealName, unicode.IsControl) >= 0 {
		writeError(w, 400, "真实姓名必填，不能超过 80 个字符或包含控制字符")
		return
	}
	if !validPassword(in.Password, "user") {
		writeError(w, 400, "密码需为 8–72 字节")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	id, err := a.store.RegisterUser(r.Context(), in.Email, in.RealName, string(hash))
	if errors.Is(err, store.ErrUserExists) {
		writeError(w, 409, err.Error())
		return
	}
	if errors.Is(err, store.ErrRegistrationUnavailable) {
		writeError(w, 403, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "注册失败，请稍后重试")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "email": in.Email})
}
