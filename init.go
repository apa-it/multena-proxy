package main

import (
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/MicahParks/keyfunc/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/go-sql-driver/mysql"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

var (
	Commit              string
	DB                  *sql.DB
	Jwks                *keyfunc.JWKS
	ServiceAccountToken string
	Logger              *zap.Logger
	Cfg                 *Config
	V                   *viper.Viper
)

func init() {
	InitConfig()
	InitLogging()
}

func doInit() {
	Logger.Info("-------Init Proxy-------")
	Logger.Info("Commit: ", zap.String("commit", Commit))
	Logger.Info("Set http client to ignore self signed certificates")
	Logger.Info("Config ", zap.Any("cfg", Cfg))
	ServiceAccountToken = Cfg.Dev.ServiceAccountToken
	if !Cfg.Dev.Enabled {
		sa, err := os.ReadFile("/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			Logger.Panic("Error while reading service account token", zap.Error(err))
		}
		ServiceAccountToken = string(sa)
	}
	if !Cfg.Dev.Enabled {
		InitJWKS()
	}

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	if Cfg.Db.Enabled {
		InitDB()
	}
	Logger.Info("------Init Complete------")
}

func InitConfig() {
	Cfg = &Config{}
	V = viper.NewWithOptions(viper.KeyDelimiter("::"))
	loadConfig("config")
	loadConfig("labels")
}

func onConfigChange(e fsnotify.Event) {
	//Todo: change log level on reload
	Cfg = &Config{}
	configs := []string{"config", "labels"}
	for _, name := range configs {
		V.SetConfigName(name) // name of config file (without extension)
		err := V.MergeInConfig()
		if err != nil { // Handle errors reading the config file
			panic(fmt.Errorf("fatal error config file: %w", err))
		}
		err = V.Unmarshal(Cfg)
		if err != nil { // Handle errors reading the config file
			panic(fmt.Errorf("fatal error config file: %w", err))
		}
	}
	fmt.Printf("{\"level\":\"info\",\"config\":\"%+v/\"}", Cfg)
	fmt.Printf("{\"level\":\"info\",\"message\":\"Config file changed: %s/\"}", e.Name)
}

func loadConfig(configName string) {
	V.SetConfigName(configName) // name of config file (without extension)
	V.SetConfigType("yaml")
	fmt.Printf("{\"level\":\"info\",\"message\":\"Looking for config in /etc/config/%s/\"}\n", configName)
	V.AddConfigPath(fmt.Sprintf("/etc/config/%s/", configName))
	V.AddConfigPath("./configs")
	err := V.MergeInConfig() // Find and read the config file
	if err != nil {          // Handle errors reading the config file
		panic(fmt.Errorf("fatal error config file: %w", err))
	}
	err = V.Unmarshal(Cfg)
	if err != nil { // Handle errors reading the config file
		panic(fmt.Errorf("fatal error config file: %w", err))
	}
	V.OnConfigChange(onConfigChange)
	V.WatchConfig()
}

// InitLogging initializes the logger
// The log level is set in the config file
// The log level can be set to debug, info, warn, error, dpanic, panic, or fatal
func InitLogging() *zap.Logger {
	rawJSON := []byte(`{
		"level": "` + strings.ToLower(Cfg.Proxy.LogLevel) + `",
		"encoding": "json",
		"outputPaths": ["stdout"],
		"errorOutputPaths": ["stdout"],
		"encoderConfig": {
		  "messageKey": "message",
		  "levelKey": "level",
		  "levelEncoder": "lowercase"
		}
	  }`)

	var cfg zap.Config
	if err := json.Unmarshal(rawJSON, &cfg); err != nil {
		panic(err)
	}
	Logger = zap.Must(cfg.Build())

	Logger.Debug("logger construction succeeded")
	Logger.Debug("Go Version", zap.String("version", runtime.Version()))
	Logger.Debug("Go OS/Arch", zap.String("os", runtime.GOOS), zap.String("arch", runtime.GOARCH))
	Logger.Debug("Config", zap.Any("cfg", Cfg))
	return Logger
}

func InitJWKS() {
	Logger.Info("Init Keycloak config")
	jwksURL := Cfg.Proxy.JwksCertURL

	options := keyfunc.Options{
		RefreshErrorHandler: func(err error) {
			if err != nil {
				Logger.Error("Error serving Keyfunc", zap.Error(err))
			}
		},
		RefreshInterval:   time.Hour,
		RefreshRateLimit:  time.Minute * 5,
		RefreshTimeout:    time.Second * 10,
		RefreshUnknownKID: true,
	}

	// Create the JWKS from the resource at the given URL.
	err := error(nil)
	Jwks, err = keyfunc.Get(jwksURL, options)
	if err != nil {
		Logger.Panic("Error init jwks", zap.Error(err))
	}
	Logger.Info("Finished Keycloak config")
}

func InitDB() {
	if Cfg.Db.Enabled {
		password, err := os.ReadFile(Cfg.Db.PasswordPath)
		if err != nil {
			Logger.Panic("Could not read db password", zap.Error(err))
		}
		cfg := mysql.Config{
			User:                 Cfg.Db.User,
			Passwd:               string(password),
			Net:                  "tcp",
			AllowNativePasswords: true,
			Addr:                 Cfg.Db.Host + ":" + fmt.Sprint(Cfg.Db.Port),
			DBName:               Cfg.Db.DbName,
		}
		// Get a database handle.
		DB, err = sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			Logger.Panic("Error opening DB connection", zap.Error(err))
		}
	}
}
