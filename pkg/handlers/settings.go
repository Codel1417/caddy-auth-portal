// Copyright 2020 Paul Greenberg greenpau@outlook.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	jwtclaims "github.com/greenpau/caddy-auth-jwt/pkg/claims"
	"github.com/greenpau/caddy-auth-portal/pkg/backends"
	"github.com/greenpau/caddy-auth-portal/pkg/ui"
	"github.com/greenpau/caddy-auth-portal/pkg/utils"
	"go.uber.org/zap"
)

// ServeSettings returns authenticated user information.
func ServeSettings(w http.ResponseWriter, r *http.Request, opts map[string]interface{}) error {
	var codeURI string
	var codeErr error
	var backend *backends.Backend
	authURLPath := opts["auth_url_path"].(string)
	if !opts["authenticated"].(bool) {
		w.Header().Set("Location", authURLPath+"?redirect_url="+r.RequestURI)
		w.WriteHeader(302)
		return nil
	}
	reqID := opts["request_id"].(string)
	log := opts["logger"].(*zap.Logger)
	claims := opts["user_claims"].(*jwtclaims.UserClaims)
	uiFactory := opts["ui"].(*ui.UserInterfaceFactory)
	if _, exists := opts["backend"]; exists {
		backend = opts["backend"].(*backends.Backend)
	}
	view := strings.TrimPrefix(r.URL.Path, authURLPath)
	view = strings.TrimPrefix(view, "/")
	view = strings.TrimPrefix(view, "settings")
	view = strings.TrimPrefix(view, "/")
	viewParts := strings.Split(view, "/")
	view = viewParts[0]
	if view == "" {
		view = "general"
	}

	// Display main authentication portal page
	resp := uiFactory.GetArgs()
	resp.Title = "Settings"

	switch view {
	case "mfa":
		if len(viewParts) > 1 {
			switch viewParts[1] {
			case "barcode":
				if len(viewParts) < 3 {
					log.Error("Failed rendering key code URI barcode", zap.String("request_id", reqID), zap.String("error", "malformed barcode url"))
					w.Header().Set("Content-Type", "text/plain")
					w.WriteHeader(400)
					w.Write([]byte(`Bad Request`))
					return fmt.Errorf("malformed barcode url")
				}
				opts["code_uri_encoded"] = strings.TrimSuffix(strings.Join(viewParts[2:], "/"), ".png")
				return ServeBarcodeImage(w, r, opts)
			case "add":
				if r.Method == "POST" {
					resp.Data["status"] = "FAIL"
					if backend != nil {
						if secrets, err := validateAddMfaTokenForm(r); err != nil {
							resp.Data["status"] = "FAIL"
							resp.Data["status_reason"] = fmt.Sprintf("Bad Request: %s", err)
						} else {
							operation := make(map[string]interface{})
							operation["name"] = "add_mfa_token"
							operation["username"] = claims.Subject
							operation["email"] = claims.Email
							for k, v := range secrets {
								operation[k] = v
							}
							if err := backend.Do(operation); err != nil {
								resp.Data["status_reason"] = fmt.Sprintf("%s", err)
							} else {
								resp.Data["status"] = "SUCCESS"
								resp.Data["status_reason"] = "MFA token has been added"
							}
						}
					} else {
						resp.Data["status_reason"] = "Authentication backend not found"
					}
					// view: mfa-add-app-status
					view = strings.Join(viewParts, "-") + "-status"
				} else {
					if len(viewParts) > 2 {
						if viewParts[2] == "app" {
							secret := utils.GetRandomEncodedStringFromRange(64, 92)
							codeOpts := make(map[string]interface{})
							codeOpts["secret"] = secret
							codeOpts["type"] = "totp"
							codeOpts["label"] = "Gatekeeper:" + claims.Email
							codeOpts["period"] = 30
							codeOpts["issuer"] = "Gatekeeper"

							resp.Data["mfa_type"] = "totp"
							resp.Data["mfa_secret"] = "secret"
							resp.Data["mfa_period"] = "30"

							// codeOpts["algorithm"] = "SHA512"
							// codeOpts["digits"] = 8
							codeURI, codeErr = utils.GetCodeURI(codeOpts)
							if codeErr != nil {
								log.Error("Failed creating key code URI", zap.String("request_id", reqID), zap.String("error", codeErr.Error()))
								w.Header().Set("Content-Type", "text/plain")
								w.WriteHeader(500)
								w.Write([]byte(`Internal Server Error`))
								return codeErr
							}
							resp.Data["code_uri"] = codeURI
							resp.Data["code_uri_encoded"] = base64.StdEncoding.EncodeToString([]byte(codeURI))
						}
					}
					view = strings.Join(viewParts, "-")
				}
			case "delete":
				view = viewParts[0] + "-" + viewParts[1] + "-status"
				resp.Data["status"] = "FAIL"
				tokenID := viewParts[2]
				if len(viewParts) != 3 {
					resp.Data["status_reason"] = "malformed request"
				} else {
					if tokenID == "" {
						resp.Data["status_reason"] = "token id not found"
					} else {
						operation := make(map[string]interface{})
						operation["name"] = "delete_mfa_token"
						operation["token_id"] = tokenID
						operation["username"] = claims.Subject
						operation["email"] = claims.Email
						if err := backend.Do(operation); err != nil {
							resp.Data["status_reason"] = fmt.Sprintf("failed deleting token id %s: %s", tokenID, err)
						} else {
							resp.Data["status"] = "SUCCESS"
							resp.Data["status_reason"] = fmt.Sprintf("token id %s deleted successfully", tokenID)
						}
					}
				}
			}
		} else {
			// Entry Page
			args := make(map[string]interface{})
			args["username"] = claims.Subject
			args["email"] = claims.Email
			mfaTokens, err := backend.GetMfaTokens(args)
			if err != nil {
				resp.Data["status"] = "failure"
				resp.Data["status_reason"] = fmt.Sprintf("%s", err)
			} else {
				if len(mfaTokens) > 0 {
					resp.Data["mfa_tokens"] = mfaTokens
				}
			}
		}
	case "password":
		if len(viewParts) > 1 {
			switch viewParts[1] {
			case "edit":
				if r.Method == "POST" {
					resp.Data["status"] = "failure"
					if backend != nil {
						if secrets, err := validatePasswordChangeForm(r); err != nil {
							resp.Data["status"] = "failure"
							resp.Data["status_reason"] = "Bad Request"
						} else {
							operation := make(map[string]interface{})
							operation["name"] = "password_change"
							operation["username"] = claims.Subject
							operation["email"] = claims.Email
							for k, v := range secrets {
								operation[k] = v
							}
							if err := backend.Do(operation); err != nil {
								resp.Data["status_reason"] = fmt.Sprintf("%s", err)
							} else {
								resp.Data["status"] = "success"
								resp.Data["status_reason"] = "Password has been changed"
							}
						}
					} else {
						resp.Data["status_reason"] = "Authentication backend not found"
					}
					view = strings.Join(viewParts, "-")
				} else {
					view = viewParts[0]
				}
			default:
				view = strings.Join(viewParts, "-")
			}
		}
	case "sshkeys", "gpgkeys":
		if len(viewParts) > 1 {
			switch viewParts[1] {
			case "add":
				if r.Method == "POST" {
					resp.Data["status"] = "FAIL"
					if backend != nil {
						if keys, err := validateKeyInputForm(r); err != nil {
							resp.Data["status"] = "FAIL"
							resp.Data["status_reason"] = "Bad Request"
						} else {
							operation := make(map[string]interface{})
							switch view {
							case "sshkeys":
								operation["name"] = "add_ssh_key"
							case "gpgkeys":
								operation["name"] = "add_gpg_key"
							}
							operation["username"] = claims.Subject
							operation["email"] = claims.Email
							for k, v := range keys {
								operation[k] = v
							}
							if err := backend.Do(operation); err != nil {
								resp.Data["status_reason"] = fmt.Sprintf("%s", err)
							} else {
								resp.Data["status"] = "SUCCESS"
								switch view {
								case "sshkeys":
									resp.Data["status_reason"] = "Public SSH key has been added"
								case "gpgkeys":
									resp.Data["status_reason"] = "GPG key has been added"
								}
							}
						}
					} else {
						resp.Data["status_reason"] = "Authentication backend not found"
					}
					view = strings.Join(viewParts, "-") + "-status"
				} else {
					view = strings.Join(viewParts, "-")
				}
			case "delete":
				view = viewParts[0] + "-" + viewParts[1] + "-status"
				resp.Data["status"] = "FAIL"
				keyID := viewParts[2]
				if len(viewParts) != 3 {
					resp.Data["status_reason"] = "malformed request"
				} else {
					if keyID == "" {
						resp.Data["status_reason"] = "key id not found"
					} else {
						operation := make(map[string]interface{})
						operation["name"] = "delete_public_key"
						operation["key_id"] = keyID
						operation["username"] = claims.Subject
						operation["email"] = claims.Email
						if err := backend.Do(operation); err != nil {
							resp.Data["status_reason"] = fmt.Sprintf("failed deleting key id %s: %s", keyID, err)
						} else {
							resp.Data["status"] = "SUCCESS"
							resp.Data["status_reason"] = fmt.Sprintf("key id %s deleted successfully", keyID)
						}
					}
				}
			default:
				view = strings.Join(viewParts, "-")
			}
		} else {
			// Entry Page
			args := make(map[string]interface{})
			args["username"] = claims.Subject
			args["email"] = claims.Email
			switch view {
			case "sshkeys":
				args["key_usage"] = "ssh"
			case "gpgkeys":
				args["key_usage"] = "gpg"
			}
			pubKeys, err := backend.GetPublicKeys(args)
			if err != nil {
				resp.Data["status"] = "failure"
				resp.Data["status_reason"] = fmt.Sprintf("%s", err)
			} else {
				if len(pubKeys) > 0 {
					resp.Data[view] = pubKeys
				}
			}
		}
	case "apikeys":
		if len(viewParts) > 1 {
			switch viewParts[1] {
			case "add":
				if r.Method == "POST" {
					resp.Data["status"] = "failure"
					if backend != nil {
						if keys, err := validateKeyInputForm(r); err != nil {
							resp.Data["status"] = "failure"
							resp.Data["status_reason"] = "Bad Request"
						} else {
							operation := make(map[string]interface{})
							operation["name"] = "add_api_key"
							operation["username"] = claims.Subject
							operation["email"] = claims.Email
							for k, v := range keys {
								operation[k] = v
							}
							if err := backend.Do(operation); err != nil {
								resp.Data["status_reason"] = fmt.Sprintf("%s", err)
							} else {
								resp.Data["status"] = "success"
								resp.Data["status_reason"] = "API key has been added"
							}
						}
					} else {
						resp.Data["status_reason"] = "Authentication backend not found"
					}
					view = strings.Join(viewParts, "-") + "-status"
				} else {
					view = strings.Join(viewParts, "-")
				}
			default:
				view = strings.Join(viewParts, "-")
			}
		}
	}

	resp.Data["view"] = view

	content, err := uiFactory.Render("settings", resp)
	if err != nil {
		log.Error("Failed HTML response rendering", zap.String("request_id", reqID), zap.String("error", err.Error()))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(500)
		w.Write([]byte(`Internal Server Error`))
		return err
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(200)
	w.Write(content.Bytes())
	return nil
}

func validatePasswordChangeForm(r *http.Request) (map[string]string, error) {
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		return nil, fmt.Errorf("Unsupported content type")
	}
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("Failed parsing submitted form")
	}
	for _, k := range []string{"secret1", "secret2", "secret3"} {
		if r.PostFormValue(k) == "" {
			return nil, fmt.Errorf("Required form field not found")
		}
	}
	if r.PostFormValue("secret1") == "" {
		return nil, fmt.Errorf("Current password is empty")
	}
	if r.PostFormValue("secret2") == "" {
		return nil, fmt.Errorf("New password is empty")
	}
	if r.PostFormValue("secret2") != r.PostFormValue("secret3") {
		return nil, fmt.Errorf("New password mismatch")
	}
	if r.PostFormValue("secret1") == r.PostFormValue("secret2") {
		return nil, fmt.Errorf("New password matches current password")
	}
	resp := make(map[string]string)
	resp["current_password"] = r.PostFormValue("secret1")
	resp["new_password"] = r.PostFormValue("secret2")
	return resp, nil
}

func validateKeyInputForm(r *http.Request) (map[string]string, error) {
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		return nil, fmt.Errorf("Unsupported content type")
	}
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("Failed parsing submitted form")
	}
	for _, k := range []string{"key1"} {
		if r.PostFormValue(k) == "" {
			return nil, fmt.Errorf("Required form field not found")
		}
	}
	if r.PostFormValue("key1") == "" {
		return nil, fmt.Errorf("Input is empty")
	}
	resp := make(map[string]string)
	resp["key"] = r.PostFormValue("key1")
	comment := r.PostFormValue("comment1")
	comment = strings.TrimSpace(comment)
	if comment != "" {
		resp["comment"] = comment
	}
	return resp, nil
}

func validateAddMfaTokenForm(r *http.Request) (map[string]string, error) {
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		return nil, fmt.Errorf("Unsupported content type")
	}
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("Failed parsing submitted form")
	}
	for _, k := range []string{"code1", "code2", "secret", "type"} {
		if r.PostFormValue(k) == "" {
			return nil, fmt.Errorf("Required form field not found")
		}
	}
	var code1, code2 string
	for _, i := range []string{"1", "2"} {
		code := r.PostFormValue("code" + i)
		if code == "" {
			return nil, fmt.Errorf("MFA code %s is empty", i)
		}
		if len(code) < 4 || len(code) > 8 {
			return nil, fmt.Errorf("MFA code %s is not 4-8 characters", i)
		}
		if i == "1" {
			code1 = code
			continue
		}
		code2 = code
		if code2 == code1 {
			return nil, fmt.Errorf("MFA code 1 and 2 match")
		}
	}

	secret := r.PostFormValue("secret")
	if secret == "" {
		return nil, fmt.Errorf("MFA secret is empty")
	}

	secretType := r.PostFormValue("type")
	switch secretType {
	case "":
		return nil, fmt.Errorf("MFA type is empty")
	case "totp":
	default:
		return nil, fmt.Errorf("MFA type is unsupported")
	}

	period := r.PostFormValue("period")
	if period == "" {
		return nil, fmt.Errorf("MFA period is empty")
	}
	periodInt, err := strconv.Atoi(period)
	if err != nil {
		return nil, fmt.Errorf("MFA period is invalid")
	}
	if period != strconv.Itoa(periodInt) {
		return nil, fmt.Errorf("MFA period is invalid")
	}
	if periodInt < 30 || periodInt > 180 {
		return nil, fmt.Errorf("MFA period is invalid")
	}

	resp := make(map[string]string)
	resp["code1"] = code1
	resp["code2"] = code2
	resp["secret"] = secret
	resp["type"] = secretType
	resp["period"] = period
	return resp, nil
}
