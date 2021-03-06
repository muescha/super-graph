package serv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/adjust/gorails/marshal"
	"github.com/adjust/gorails/session"
	"github.com/bradfitz/gomemcache/memcache"
	"github.com/garyburd/redigo/redis"
)

func railsRedisHandler(next http.HandlerFunc) http.HandlerFunc {
	cookie := conf.GetString("auth.cookie")
	if len(cookie) == 0 {
		panic(errors.New("no auth.cookie defined"))
	}

	conf.BindEnv("auth.url", "SG_AUTH_URL")
	authURL := conf.GetString("auth.url")
	if len(authURL) == 0 {
		panic(errors.New("no auth.url defined"))
	}

	conf.SetDefault("auth.max_idle", 80)
	conf.SetDefault("auth.max_active", 12000)

	rp := &redis.Pool{
		MaxIdle:   conf.GetInt("auth.max_idle"),
		MaxActive: conf.GetInt("auth.max_active"),
		Dial: func() (redis.Conn, error) {
			c, err := redis.DialURL(authURL)
			if err != nil {
				panic(err)
			}

			conf.BindEnv("auth.password", "SG_AUTH_PASSWORD")
			pwd := conf.GetString("auth.password")
			if len(pwd) != 0 {
				if _, err := c.Do("AUTH", pwd); err != nil {
					panic(err)
				}
			}
			return c, err
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(cookie)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		key := fmt.Sprintf("session:%s", ck.Value)
		sessionData, err := redis.Bytes(rp.Get().Do("GET", key))
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		userID, err := railsAuth(string(sessionData), emptySecret)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func railsMemcacheHandler(next http.HandlerFunc) http.HandlerFunc {
	cookie := conf.GetString("auth.cookie")
	if len(cookie) == 0 {
		panic(errors.New("no auth.cookie defined"))
	}

	host := conf.GetString("auth.host")
	if len(host) == 0 {
		panic(errors.New("no auth.host defined"))
	}

	mc := memcache.New(host)

	return func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(cookie)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		key := fmt.Sprintf("session:%s", ck.Value)
		item, err := mc.Get(key)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		userID, err := railsAuth(string(item.Value), emptySecret)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func railsCookieHandler(next http.HandlerFunc) http.HandlerFunc {
	cookie := conf.GetString("auth.cookie")
	if len(cookie) == 0 {
		panic(errors.New("no auth.cookie defined"))
	}

	conf.BindEnv("auth.secret_key_base", "SG_AUTH_SECRET_KEY_BASE")
	secret := conf.GetString("auth.secret_key_base")
	if len(secret) == 0 {
		panic(errors.New("no auth.secret_key_base defined"))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(cookie)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		userID, err := railsAuth(ck.Value, secret)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func railsAuth(cookie, secret string) (userID string, err error) {
	var dcookie []byte

	if len(secret) != 0 {
		dcookie, err = session.DecryptSignedCookie(cookie, secret, salt, signSalt)
		if err != nil {
			return
		}
	}

	if dcookie[0] != '{' {
		userID, err = getUserId4(dcookie)
	} else {
		userID, err = getUserId(dcookie)
	}

	return
}

func getUserId(data []byte) (userID string, err error) {
	var sessionData map[string]interface{}

	err = json.Unmarshal(data, &sessionData)
	if err != nil {
		return
	}

	userKey, ok := sessionData["warden.user.user.key"]
	if !ok {
		err = errors.New("key 'warden.user.user.key' not found in session data")
	}

	items, ok := userKey.([]interface{})
	if !ok {
		err = errSessionData
		return
	}

	if len(items) != 2 {
		err = errSessionData
		return
	}

	uids, ok := items[0].([]interface{})
	if !ok {
		err = errSessionData
		return
	}

	uid, ok := uids[0].(float64)
	if !ok {
		err = errSessionData
		return
	}
	userID = fmt.Sprintf("%d", int64(uid))

	return
}

func getUserId4(data []byte) (userID string, err error) {
	sessionData, err := marshal.CreateMarshalledObject(data).GetAsMap()
	if err != nil {
		return
	}

	wardenData, ok := sessionData["warden.user.user.key"]
	if !ok {
		err = errSessionData
		return
	}

	wardenUserKey, err := wardenData.GetAsArray()
	if err != nil {
		return
	}
	if len(wardenUserKey) < 1 {
		err = errSessionData
		return
	}

	userData, err := wardenUserKey[0].GetAsArray()
	if err != nil {
		return
	}
	if len(userData) < 1 {
		err = errSessionData
		return
	}

	uid, err := userData[0].GetAsInteger()
	if err != nil {
		return
	}
	userID = fmt.Sprintf("%d", uid)

	return
}
