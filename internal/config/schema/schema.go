package schema

import (
	"time"

	"github.com/depthbomb/envschema"
)

type Environment struct{}

func (Environment) EnvSchema() envschema.Schema {
	positiveDuration := envschema.Duration().AtLeastDuration(time.Millisecond)
	positiveInt := envschema.Int().AtLeast(1)

	return envschema.Must(
		envschema.Named("DATABASE_PATH", "DatabasePath", envschema.String().DefaultTo("")),
		envschema.Named("BOT_TOKEN", "BotToken", envschema.Secret().Optional()),
		envschema.Named("STEAM_WEB_API_KEY", "SteamAPIKey", envschema.Secret().Optional()),
		envschema.Named("DISCORD_APPLICATION_ID", "ApplicationID", envschema.String().Optional()),
		envschema.Var("DIAGNOSTICS_ENABLED", envschema.Boolean().DefaultTo(false)),
		envschema.Var("DIAGNOSTICS_DIRECTORY", envschema.String().DefaultTo("diagnostics")),
		envschema.Var("DEFAULT_COUNTRY", envschema.String().Matching(`^[A-Z]{2}$`).DefaultTo("US")),
		envschema.Var("POLL_INTERVAL", positiveDuration.DefaultTo(time.Hour)),
		envschema.Var("POLL_BATCH_SIZE", positiveInt.AtMost(100).DefaultTo(10)),
		envschema.Var("STEAM_TIMEOUT", positiveDuration.DefaultTo(15*time.Second)),
		envschema.Var("DELIVERY_POLL_INTERVAL", positiveDuration.DefaultTo(2*time.Second)),
		envschema.Var("DELIVERY_RETRY_INITIAL_DELAY", positiveDuration.DefaultTo(5*time.Second)),
		envschema.Var("DELIVERY_RETRY_MAXIMUM_DELAY", positiveDuration.DefaultTo(15*time.Minute)),
		envschema.Var("DELIVERY_RETRY_MAXIMUM_AGE", positiveDuration.DefaultTo(72*time.Hour)),
		envschema.Var("CATALOG_SYNC_INTERVAL", positiveDuration.DefaultTo(24*time.Hour)),
		envschema.Var("COMMAND_TIMEOUT", positiveDuration.DefaultTo(45*time.Second)),
		envschema.Var("COMMAND_RATE_LIMIT", positiveInt.DefaultTo(20)),
		envschema.Var("COMMAND_RATE_WINDOW", positiveDuration.DefaultTo(time.Minute)),
		envschema.Var("MAXIMUM_ACTIVE_TRACKS_PER_USER", positiveInt.DefaultTo(25)),
		envschema.Var("PRICE_RATE_LIMIT", positiveInt.DefaultTo(5)),
		envschema.Var("PRICE_RATE_WINDOW", positiveDuration.DefaultTo(time.Minute)),
		envschema.Var("TRACK_ADD_RATE_LIMIT", positiveInt.DefaultTo(10)),
		envschema.Var("TRACK_ADD_RATE_WINDOW", positiveDuration.DefaultTo(time.Hour)),
		envschema.Var("STEAM_MAXIMUM_CONCURRENCY", positiveInt.DefaultTo(8)),
		envschema.Named("PRICE_CACHE_TTL", "PriceCacheTTL", positiveDuration.DefaultTo(2*time.Minute)),
		envschema.Var("PRICE_CACHE_MAXIMUM_ENTRIES", positiveInt.DefaultTo(10000)),
		envschema.Named("ARTWORK_CACHE_TTL", "ArtworkCacheTTL", positiveDuration.DefaultTo(24*time.Hour)),
		envschema.Var("ARTWORK_CACHE_RETRY_INTERVAL", positiveDuration.DefaultTo(5*time.Minute)),
		envschema.Var("ARTWORK_CACHE_MAXIMUM_ENTRIES", positiveInt.DefaultTo(10000)),
		envschema.Var("STEAM_CIRCUIT_FAILURE_THRESHOLD", positiveInt.DefaultTo(5)),
		envschema.Var("STEAM_CIRCUIT_OPEN_DURATION", positiveDuration.DefaultTo(30*time.Second)),
		envschema.Var("PRICE_CHECKS_ENABLED", envschema.Boolean().DefaultTo(true)),
		envschema.Var("NEW_TRACKS_ENABLED", envschema.Boolean().DefaultTo(true)),
		envschema.Var("POLLING_ENABLED", envschema.Boolean().DefaultTo(true)),
		envschema.Var("DELIVERY_ENABLED", envschema.Boolean().DefaultTo(true)),
		envschema.Var("FREE_GAMES_ENABLED", envschema.Boolean().DefaultTo(true)),
		envschema.Var("FREE_GAMES_POLL_INTERVAL", envschema.Duration().AtLeastDuration(time.Hour).DefaultTo(time.Hour)),
		envschema.Var("FREE_GAMES_UBISOFT_INTERVAL", envschema.Duration().AtLeastDuration(4*time.Hour).DefaultTo(6*time.Hour)),
		envschema.Var("RETENTION_INTERVAL", positiveDuration.DefaultTo(24*time.Hour)),
		envschema.Var("OBSERVATION_RETENTION", positiveDuration.DefaultTo(720*time.Hour)),
		envschema.Var("DELIVERY_RETENTION", positiveDuration.DefaultTo(2160*time.Hour)),
		envschema.Var("SUBSCRIPTION_RETENTION", positiveDuration.DefaultTo(720*time.Hour)),
	).LessThanVariable("DELIVERY_RETRY_INITIAL_DELAY", "DELIVERY_RETRY_MAXIMUM_DELAY").LessThanVariable("DELIVERY_RETRY_MAXIMUM_DELAY", "DELIVERY_RETRY_MAXIMUM_AGE")
}
