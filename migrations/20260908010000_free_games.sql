CREATE TABLE "free_subscriptions" (
  "id" varchar(24) NOT NULL PRIMARY KEY,
  "scope" varchar NOT NULL,
  "source" varchar NOT NULL,
  "destination_kind" varchar NOT NULL,
  "destination_id" varchar NOT NULL,
  "managed_by" varchar NOT NULL,
  "enabled" boolean NOT NULL DEFAULT true,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL
);
CREATE UNIQUE INDEX "freesubscription_scope_source" ON "free_subscriptions" ("scope", "source");
CREATE INDEX "freesubscription_source_enabled" ON "free_subscriptions" ("source", "enabled");

CREATE TABLE "free_offers" (
  "id" varchar(24) NOT NULL PRIMARY KEY,
  "identity" varchar NOT NULL,
  "source" varchar NOT NULL,
  "payload" jsonb NOT NULL,
  "eligible" boolean NOT NULL DEFAULT false,
	"closed" boolean NOT NULL DEFAULT false,
	"generation" bigint NOT NULL DEFAULT 1,
  "first_seen_at" timestamptz NOT NULL,
  "last_seen_at" timestamptz NOT NULL
);
CREATE UNIQUE INDEX "free_offers_identity_key" ON "free_offers" ("identity");
CREATE INDEX "freeoffer_source_eligible" ON "free_offers" ("source", "eligible");

CREATE TABLE "free_deliveries" (
  "id" varchar(24) NOT NULL PRIMARY KEY,
  "offer_id" varchar NOT NULL,
  "subscription_id" varchar NOT NULL,
	"offer_generation" bigint NOT NULL DEFAULT 1,
  "destination_kind" varchar NOT NULL,
  "destination_id" varchar NOT NULL,
  "payload" jsonb NOT NULL,
  "status" varchar NOT NULL DEFAULT 'pending',
  "attempt_count" bigint NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz NOT NULL,
  "message_id" varchar NOT NULL DEFAULT '',
  "last_error" varchar NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL
);
CREATE UNIQUE INDEX "freedelivery_offer_id_offer_generation_subscription_id" ON "free_deliveries" ("offer_id", "offer_generation", "subscription_id");
CREATE INDEX "freedelivery_status_next_attempt_at" ON "free_deliveries" ("status", "next_attempt_at");

CREATE TABLE "free_monitors" (
  "id" varchar NOT NULL PRIMARY KEY,
  "checked_at" timestamptz NULL,
  "next_check_at" timestamptz NOT NULL,
  "status" varchar NOT NULL DEFAULT 'not checked',
  "problems" jsonb NULL,
  "failures" bigint NOT NULL DEFAULT 0
);
