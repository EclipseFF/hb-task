package main

const (
	databaseURL        = "postgres://vacation:vacation@postgres:5432/vacation?sslmode=disable"
	camundaURL         = "http://camunda:8080/v2"
	camundaUser        = "manager"
	camundaPassword    = "manager"
	oidcIssuer         = "http://localhost:8081/realms/vacation"
	oidcJWKS           = "http://keycloak:8080/realms/vacation/protocol/openid-connect/certs"
	kafkaBroker        = "kafka:9092"
	clickhouseURL      = "http://clickhouse:8123"
	clickhouseUser     = "vacation"
	clickhousePassword = "vacation"
)
