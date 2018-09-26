// Copyright (c) 2018 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:generate protoc --proto_path=model/http-security --gogo_out=model/http-security model/http-security/httpsecurity.proto

package security

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
	"github.com/ligato/cn-infra/logging"
	httpsecurity "github.com/ligato/cn-infra/rpc/rest/security/model/http-security"
	"github.com/pkg/errors"
	"github.com/unrolled/render"
	"golang.org/x/crypto/bcrypt"
)

const (
	// Helps to obtain authorization header matching the field in a request
	authHeaderStr = "authorization"
	// Admin constant, used to define admin security group and user
	admin = "admin"
	// Token header constant
	bearer = "Bearer"
)

const (
	// URL for login. Successful login returns token. Re-login invalidates old token and returns a new one.
	login = "/login"
	// URL key for logout, invalidates current token.
	logout = "/logout"
)

// Default value to sign the token, if not provided from config file
var signature = "secret"
var expTime time.Duration = 3600000000000 // 1 Hour

// AuthenticatorAPI provides methods for handling permissions
type AuthenticatorAPI interface {
	// AddPermissionGroup adds new permission group. PG is defined by name and a set of URL keys. User with
	// permission group enabled has access to that set of keys. PGs with duplicated names are skipped.
	AddPermissionGroup(group ...*httpsecurity.PermissionGroup)

	// Validate serves as middleware used while registering new HTTP handler. For every request, token
	// and permission group is validated.
	Validate(provider http.HandlerFunc) http.HandlerFunc
}

// Context defines fields required to instantiate authenticator
type Context struct {
	StorageType StorageType
	Users       []httpsecurity.User
	ExpTime     time.Duration
	Cost        int
	Signature   string
}

// Credentials struct represents user login input
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Authenticator keeps information about users, permission groups and tokens and processes it
type authenticator struct {
	log logging.Logger

	// Router instance automatically registers login/logout REST API handlers if authentication is enabled
	router    *mux.Router
	formatter *render.Render

	// User database keeps all known users with permissions and hashed password. Users are loaded from
	// HTTP config file
	// TODO add option to register users
	userDb AuthStore
	// Permission database is a map of name/permissions and bound URLs
	groupDb map[string][]*httpsecurity.PermissionGroup_Permissions
	// Token database keeps information of actual token and its owner.
	tokenDb map[string]string

	// Token claims
	expTime time.Duration
}

// NewAuthenticator prepares new instance of authenticator.
func NewAuthenticator(router *mux.Router, ctx *Context, log logging.Logger) AuthenticatorAPI {
	a := &authenticator{
		router: router,
		log:    log,
		formatter: render.New(render.Options{
			IndentJSON: true,
		}),
		userDb:  CreateAuthStore(ctx.StorageType),
		groupDb: make(map[string][]*httpsecurity.PermissionGroup_Permissions),
		tokenDb: make(map[string]string),
		expTime: ctx.ExpTime,
	}

	// Set token signature
	signature = ctx.Signature
	if a.expTime == 0 {
		a.expTime = expTime
		a.log.Debugf("Token expiration time claim not set, defaulting to 1 hour")
	}

	// Add admin-user, enabled by default, always has access to every URL
	hash, err := bcrypt.GenerateFromPassword([]byte("ligato123"), ctx.Cost)
	if err != nil {
		a.log.Errorf("failed to hash password for admin: %v", err)
	}
	a.userDb.AddUser(admin, string(hash), []string{admin})

	// Process users in go routine, since hashing may take some time
	go func() {
		for _, user := range ctx.Users {
			if user.Name == admin {
				a.log.Errorf("rejected to create user-defined account named 'admin'")
				continue
			}
			if err := a.userDb.AddUser(user.Name, user.PasswordHash, user.Permissions); err != nil {
				a.log.Errorf("failed to add user %s: %v", user.Name, err)
				continue
			}
			a.log.Warnf("Registered user %s, permissions: %v", user.Name, user.Permissions)
		}
	}()

	// Admin-group, available by default and always enabled for all URLs
	a.groupDb[admin] = []*httpsecurity.PermissionGroup_Permissions{}

	a.registerSecurityHandlers()

	return a
}

// AddPermissionGroup adds new permission group.
func (a *authenticator) AddPermissionGroup(group ...*httpsecurity.PermissionGroup) {
	for _, newPermissionGroup := range group {
		if _, ok := a.groupDb[newPermissionGroup.Name]; ok {
			a.log.Warnf("permission group %s already exists, skipped")
			continue
		}
		a.log.Debugf("added HTTP permission group %s", newPermissionGroup.Name)
		a.groupDb[newPermissionGroup.Name] = newPermissionGroup.Permissions
	}
}

// Validate the request
func (a *authenticator) Validate(provider http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authHeader := req.Header.Get(authHeaderStr)
		if authHeader == "" {
			a.formatter.Text(w, http.StatusUnauthorized, "401 Unauthorized: authorization header required")
			return
		}
		bearerToken := strings.Split(authHeader, " ")
		if len(bearerToken) != 2 {
			a.formatter.Text(w, http.StatusUnauthorized, "401 Unauthorized: invalid authorization token")
			return
		}
		if bearerToken[0] != bearer {
			a.formatter.Text(w, http.StatusUnauthorized, "401 Unauthorized: invalid authorization header")
			return
		}
		token, err := jwt.Parse(bearerToken[1], func(token *jwt.Token) (interface{}, error) {
			if _, ok := jwt.GetSigningMethod(token.Header["alg"].(string)).(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("error parsing token")
			}
			return []byte(signature), nil
		})
		if err != nil {
			errStr := fmt.Sprintf("500 internal server error: %s", err)
			a.formatter.Text(w, http.StatusInternalServerError, errStr)
			return
		}
		// Validate token claims
		if token.Claims != nil {
			if err := token.Claims.Valid(); err != nil {
				errStr := fmt.Sprintf("401 Unauthorized: %v", err)
				a.formatter.Text(w, http.StatusUnauthorized, errStr)
				return
			}
		}
		// Validate token itself
		if err := a.validateToken(token.Raw, req.URL.Path, req.Method); err != nil {
			errStr := fmt.Sprintf("401 Unauthorized: %v", err)
			a.formatter.Text(w, http.StatusUnauthorized, errStr)
			return
		}

		provider.ServeHTTP(w, req)
	})
}

// Register authenticator-wide security handlers
func (a *authenticator) registerSecurityHandlers() {
	a.router.HandleFunc(login, a.createTokenEndpoint).Methods(http.MethodPost)
	a.router.HandleFunc(logout, a.invalidateTokenEndpoint).Methods(http.MethodPost)
}

// Validates credentials and provides new token
func (a *authenticator) createTokenEndpoint(w http.ResponseWriter, req *http.Request) {
	name, errCode, err := a.validateCredentials(req)
	if err != nil {
		a.formatter.Text(w, errCode, err.Error())
		return
	}
	claims := &jwt.StandardClaims{
		Audience:  name,
		ExpiresAt: a.expTime.Nanoseconds(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(signature))
	if err != nil {
		errStr := fmt.Sprintf("500 internal server error: failed to sign token: %v", err)
		a.log.Error(errStr)
		a.formatter.Text(w, http.StatusInternalServerError, errStr)
		return
	}
	a.tokenDb[name] = tokenString
	a.formatter.Text(w, http.StatusOK, tokenString)
}

// Removes token endpoint from the DB. During processing, token will not be found and will be considered as invalid.
func (a *authenticator) invalidateTokenEndpoint(w http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	var credentials credentials
	err := decoder.Decode(&credentials)
	if err != nil {
		errStr := fmt.Sprintf("500 internal server error: failed to decode json: %v", err)
		a.formatter.Text(w, http.StatusInternalServerError, errStr)
		return
	}
	delete(a.tokenDb, credentials.Username)
}

// Validates credentials, returns name and error code/message if invalid
func (a *authenticator) validateCredentials(req *http.Request) (string, int, error) {
	decoder := json.NewDecoder(req.Body)
	var credentials credentials
	err := decoder.Decode(&credentials)
	if err != nil {
		return "", http.StatusInternalServerError, errors.Errorf("500 internal server error: failed to decode json: %v", err)
	}
	user, err := a.userDb.GetUser(credentials.Username)
	if err != nil {
		return credentials.Username, http.StatusUnauthorized, errors.Errorf("401 unauthorized: user name or password is incorrect")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credentials.Password)); err != nil {
		return credentials.Username, http.StatusUnauthorized, fmt.Errorf("401 unauthorized: user name or password is incorrect")
	}
	return credentials.Username, 0, nil
}

// Validates token itself and permissions
func (a *authenticator) validateToken(token, url, method string) error {
	owner, err := a.getTokenOwner(token)
	if err != nil {
		return err
	}
	user, err := a.userDb.GetUser(owner)
	if err != nil {
		return fmt.Errorf("failed to validate token: %v", err)
	}
	// Do not check for permissions if user is admin
	if userIsAdmin(user) {
		return nil
	}

	perms := a.getPermissionsForURL(url, method)
	for _, userPerm := range user.Permissions {
		for _, perm := range perms {
			if userPerm == perm {
				return nil
			}
		}
	}

	return fmt.Errorf("not permitted")
}

// Returns token owner, or error if not found
func (a *authenticator) getTokenOwner(token string) (string, error) {
	for name, knownToken := range a.tokenDb {
		if token == knownToken {
			return name, nil
		}
	}
	return "", fmt.Errorf("authorization token is invalid")
}

// Returns all permission groups provided URL/Method is allowed for
func (a *authenticator) getPermissionsForURL(url, method string) []string {
	var groups []string
	for groupName, permissions := range a.groupDb {
		for _, permissions := range permissions {
			// Check URL
			if permissions.Url == url {
				// Check allowed methods
				for _, allowed := range permissions.AllowedMethods {
					if allowed == method {
						groups = append(groups, groupName)
					}
				}
			}
		}
	}
	return groups
}

// Checks user admin permission
func userIsAdmin(user *httpsecurity.User) bool {
	for _, permission := range user.Permissions {
		if permission == admin {
			return true
		}
	}
	return false
}
