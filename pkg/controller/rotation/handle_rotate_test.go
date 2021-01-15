// Copyright 2021 Google LLC
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

package rotation

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/exposure-notifications-server/pkg/keys"
	"github.com/google/exposure-notifications-verification-server/internal/project"
	"github.com/google/exposure-notifications-verification-server/pkg/config"
	"github.com/google/exposure-notifications-verification-server/pkg/database"
	"github.com/google/exposure-notifications-verification-server/pkg/render"
)

func Test_shouldRotate(t *testing.T) {
	t.Parallel()

	ttl := 1 * time.Second

	ctx := project.TestContext(t)
	db, _ := testDatabaseInstance.NewDatabase(t, nil)
	config := &config.RotationConfig{
		MinTTL: ttl,
	}
	c := New(config, db, nil, nil)

	if ok, err := c.shouldRotate(ctx); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatalf("failed to claim lock when available")
	}

	if ok, err := c.shouldRotate(ctx); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("allowed to claim lock when it should not be available")
	}

	time.Sleep(ttl)

	if ok, err := c.shouldRotate(ctx); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatalf("failed to claim lock when available")
	}
}

func TestHandleRotate(t *testing.T) {
	t.Parallel()

	ctx := project.TestContext(t)

	db, _ := testDatabaseInstance.NewDatabase(t, nil)

	keyManager := keys.TestKeyManager(t)
	keyManagerSigner, ok := keyManager.(keys.SigningKeyManager)
	if !ok {
		t.Fatal("kms cannot manage signing keys")
	}
	tokenSigningKey := keys.TestSigningKey(t, keyManager)

	h, err := render.New(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}

	config := &config.RotationConfig{
		TokenSigning: config.TokenSigningConfig{
			TokenSigningKeys: []string{tokenSigningKey},
		},
		TokenSigningKeyMaxAge: 30 * time.Second,
	}
	c := New(config, db, keyManagerSigner, h)

	t.Run("rotates", func(t *testing.T) {
		t.Parallel()

		tokenSigningKeyVersion, err := keyManagerSigner.CreateKeyVersion(ctx, tokenSigningKey)
		if err != nil {
			t.Fatal(err)
		}

		key := &database.TokenSigningKey{
			KeyVersionID: tokenSigningKeyVersion,
			CreatedAt:    time.Now().UTC().Add(-24 * time.Hour),
		}
		if err := db.SaveTokenSigningKey(key, database.SystemTest); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveTokenSigningKey(key, database.SystemTest); err != nil {
			t.Fatal(err)
		}
		if err := db.ActivateTokenSigningKey(key.ID, database.SystemTest); err != nil {
			t.Fatal(err)
		}

		// Rotating should create a new key.
		{
			r, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			r = r.Clone(ctx)

			w := httptest.NewRecorder()

			c.HandleRotate().ServeHTTP(w, r)

			keys, err := db.ListTokenSigningKeys()
			if err != nil {
				t.Fatal(err)
			}

			if got, want := len(keys), 2; got != want {
				t.Errorf("got %d keys, expected %d", got, want)
			}
		}

		// Rotating again should not create a new key (not enough time has elapsed
		// since TokenSigningKeyMaxAge).
		{
			r, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			r = r.Clone(ctx)

			w := httptest.NewRecorder()

			c.HandleRotate().ServeHTTP(w, r)

			keys, err := db.ListTokenSigningKeys()
			if err != nil {
				t.Fatal(err)
			}

			if got, want := len(keys), 2; got != want {
				t.Errorf("got %d keys, expected %d", got, want)
			}
		}
	})
}
