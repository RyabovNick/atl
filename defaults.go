package main

// Maintainers: fill in the company's base URLs so colleagues only need tokens.
// They apply when neither JIRA_URL/CONFLUENCE_URL nor the config file sets a
// URL, and can be overridden at build time without editing this file:
//
//	go build -ldflags "-X main.defaultJiraURL=https://jira.example.com"
var (
	defaultJiraURL       = ""
	defaultConfluenceURL = ""
)

// version is set by the Makefile and CI with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func defaultURL(name string) string {
	switch name {
	case "jira":
		return defaultJiraURL
	case "confluence":
		return defaultConfluenceURL
	}
	return ""
}

// credentialVars names the variables a user still has to set for a service,
// leaving out the URL when a built-in default covers it.
func credentialVars(name string) string {
	p := envPrefix(name)
	if defaultURL(name) != "" {
		return p + "API_TOKEN"
	}
	return p + "URL and " + p + "API_TOKEN"
}
