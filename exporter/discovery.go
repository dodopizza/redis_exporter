package exporter

import (
	"context"
	"encoding/csv"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/services/redis/mgmt/2018-03-01/redis"
	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2018-02-01/resources"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/cloudfoundry-community/go-cfenv"
	log "github.com/sirupsen/logrus"
)

// loadRedisArgs loads the configuration for which redis hosts to monitor from either
// the environment or as passed from program arguments. Returns the list of host addrs,
// passwords, and their aliases.
func LoadRedisArgs(addr, password, alias, separator string) ([]string, []string, []string) {
	if addr == "" {
		addr = "redis://localhost:6379"
	}
	addrs := strings.Split(addr, separator)
	passwords := strings.Split(password, separator)
	for len(passwords) < len(addrs) {
		passwords = append(passwords, passwords[0])
	}
	aliases := strings.Split(alias, separator)
	for len(aliases) < len(addrs) {
		aliases = append(aliases, aliases[0])
	}
	return addrs, passwords, aliases
}

// loadRedisFile opens the specified file and loads the configuration for which redis
// hosts to monitor. Returns the list of hosts addrs, passwords, and their aliases.
func LoadRedisFile(fileName string) ([]string, []string, []string, error) {
	var addrs []string
	var passwords []string
	var aliases []string
	file, err := os.Open(fileName)
	if err != nil {
		return nil, nil, nil, err
	}
	r := csv.NewReader(file)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, nil, err
	}
	file.Close()
	// For each line, test if it contains an optional password and alias and provide them,
	// else give them empty strings
	for _, record := range records {
		length := len(record)
		switch length {
		case 3:
			addrs = append(addrs, record[0])
			passwords = append(passwords, record[1])
			aliases = append(aliases, record[2])
		case 2:
			addrs = append(addrs, record[0])
			passwords = append(passwords, record[1])
			aliases = append(aliases, "")
		case 1:
			addrs = append(addrs, record[0])
			passwords = append(passwords, "")
			aliases = append(aliases, "")
		}
	}
	return addrs, passwords, aliases, nil
}

func GetCloudFoundryRedisBindings() (addrs, passwords, aliases []string) {
	if !cfenv.IsRunningOnCF() {
		return
	}

	appEnv, err := cfenv.Current()
	if err != nil {
		log.Warnln("Unable to get current CF environment", err)
		return
	}

	redisServices, err := appEnv.Services.WithTag("redis")
	if err != nil {
		log.Warnln("Error while getting redis services", err)
		return
	}

	for _, redisService := range redisServices {
		credentials := redisService.Credentials
		host := getAlternative(credentials, "host", "hostname")
		port := getAlternative(credentials, "port")
		password := getAlternative(credentials, "password")

		addr := host + ":" + port
		alias := redisService.Name

		addrs = append(addrs, addr)
		passwords = append(passwords, password)
		aliases = append(aliases, alias)
	}

	return
}

func GetAzureRedisServices() ([]string, []string, []string, error) {
	var addrs []string
	var passwords []string
	var aliases []string

	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err != nil {
		return nil, nil, nil, err
	}
	env, _ := azure.EnvironmentFromName(os.Getenv("AZURE_ENVIRONMENT"))
	if err != nil {
		return nil, nil, nil, err
	}
	redisClient := redis.NewClientWithBaseURI(env.ResourceManagerEndpoint, os.Getenv("AZURE_SUBSCRIPTION_ID"))
	redisClient.Authorizer = authorizer

	groupClient := resources.NewGroupsClientWithBaseURI(env.ResourceManagerEndpoint, os.Getenv("AZURE_SUBSCRIPTION_ID"))
	groupClient.Authorizer = authorizer

	groupsList, err := groupClient.List(context.Background(), "", nil)

	if err != nil {
		return nil, nil, nil, err
	}
	for _, resourceGroup := range groupsList.Values() {
		listResultPage, _ := redisClient.ListByResourceGroup(context.Background(), *resourceGroup.Name)
		for _, cache := range listResultPage.Values() {
			keys, _ := redisClient.ListKeys(context.Background(), *resourceGroup.Name, *cache.Name)
			EnableNonSslPort := *cache.Properties.EnableNonSslPort
			if EnableNonSslPort {
				addrs = append(addrs, "redis://"+*cache.Properties.HostName)
			} else {
				addrs = append(addrs, "rediss://"+*cache.Properties.HostName+":6380")
			}
			if keys.PrimaryKey == nil {
				log.Warnf("ERROR: You have no rights to read redis keys for %s\n", *cache.Name)
			}
			aliases = append(aliases, *cache.Name)
			passwords = append(passwords, *keys.PrimaryKey)
		}
	}
	return addrs, passwords, aliases, nil
}

func getAlternative(credentials map[string]interface{}, alternatives ...string) string {
	for _, key := range alternatives {
		if value, ok := credentials[key]; ok {
			return value.(string)
		}
	}
	return ""
}
