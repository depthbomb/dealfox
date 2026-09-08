-- Create "apps" table
CREATE TABLE "apps" (
  "id" bigint NOT NULL,
  "name" character varying NOT NULL,
  "type" character varying NOT NULL,
  "last_modified" bigint NOT NULL DEFAULT 0,
  "price_change_number" bigint NOT NULL DEFAULT 0,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);
-- Create "targets" table
CREATE TABLE "targets" (
  "id" character varying NOT NULL,
  "country" character varying NOT NULL,
  "next_due_at" timestamptz NOT NULL,
  "last_observed_at" timestamptz NULL,
  "failure_count" bigint NOT NULL DEFAULT 0,
  "last_error" character varying NOT NULL DEFAULT '',
  "app_id" bigint NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "targets_apps_targets" FOREIGN KEY ("app_id") REFERENCES "apps" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "target_country" CHECK ((country)::text ~ '^[A-Z]{2}$'::text),
  CONSTRAINT "target_failures" CHECK (failure_count >= 0)
);
-- Create index "target_app_id_country" to table: "targets"
CREATE UNIQUE INDEX "target_app_id_country" ON "targets" ("app_id", "country");
-- Create index "target_next_due_at" to table: "targets"
CREATE INDEX "target_next_due_at" ON "targets" ("next_due_at");
-- Create "observations" table
CREATE TABLE "observations" (
  "id" character varying NOT NULL,
  "observed_at" timestamptz NOT NULL,
  "price" jsonb NOT NULL,
  "target_id" character varying NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "observations_targets_observations" FOREIGN KEY ("target_id") REFERENCES "targets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "observation_observed_at" to table: "observations"
CREATE INDEX "observation_observed_at" ON "observations" ("observed_at");
-- Create index "observation_target_id_observed_at" to table: "observations"
CREATE INDEX "observation_target_id_observed_at" ON "observations" ("target_id", "observed_at");
-- Create "rules" table
CREATE TABLE "rules" (
  "id" character varying NOT NULL,
  "owner_id" character varying NOT NULL,
  "request_id" character varying NULL,
  "condition" character varying NOT NULL,
  "budget_minor" bigint NULL,
  "budget_currency" character varying NOT NULL DEFAULT '',
  "recurring" boolean NOT NULL DEFAULT false,
  "enabled" boolean NOT NULL DEFAULT true,
  "latched" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "target_id" character varying NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "rules_targets_rules" FOREIGN KEY ("target_id") REFERENCES "targets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "rule_budget" CHECK ((((condition)::text = 'any_sale'::text) AND (budget_minor IS NULL) AND ((budget_currency)::text = ''::text)) OR (((condition)::text = 'sale_under_budget'::text) AND (budget_minor IS NOT NULL) AND (budget_minor >= 0) AND ((budget_currency)::text ~ '^[A-Z]{3}$'::text)))
);
-- Create index "rule_owner_id_enabled" to table: "rules"
CREATE INDEX "rule_owner_id_enabled" ON "rules" ("owner_id", "enabled");
-- Create index "rule_owner_id_target_id_condition" to table: "rules"
CREATE UNIQUE INDEX "rule_owner_id_target_id_condition" ON "rules" ("owner_id", "target_id", "condition") WHERE enabled;
-- Create index "rule_target_id_enabled" to table: "rules"
CREATE INDEX "rule_target_id_enabled" ON "rules" ("target_id", "enabled");
-- Create index "rules_request_id_key" to table: "rules"
CREATE UNIQUE INDEX "rules_request_id_key" ON "rules" ("request_id");
-- Create "events" table
CREATE TABLE "events" (
  "id" character varying NOT NULL,
  "payload" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL,
  "observation_id" character varying NOT NULL,
  "rule_id" character varying NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "events_observations_events" FOREIGN KEY ("observation_id") REFERENCES "observations" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "events_rules_events" FOREIGN KEY ("rule_id") REFERENCES "rules" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "event_rule_id_observation_id" to table: "events"
CREATE UNIQUE INDEX "event_rule_id_observation_id" ON "events" ("rule_id", "observation_id");
-- Create "deliveries" table
CREATE TABLE "deliveries" (
  "id" character varying NOT NULL,
  "destination_id" character varying NOT NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "attempt_count" bigint NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz NOT NULL,
  "message_id" character varying NOT NULL DEFAULT '',
  "last_error" character varying NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "event_id" character varying NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "deliveries_events_deliveries" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "delivery_attempts" CHECK (attempt_count >= 0),
  CONSTRAINT "delivery_status" CHECK ((status)::text = ANY ((ARRAY['pending'::character varying, 'sending'::character varying, 'retry'::character varying, 'sent'::character varying, 'dead'::character varying, 'cancelled'::character varying])::text[]))
);
-- Create index "delivery_status_next_attempt_at" to table: "deliveries"
CREATE INDEX "delivery_status_next_attempt_at" ON "deliveries" ("status", "next_attempt_at");
