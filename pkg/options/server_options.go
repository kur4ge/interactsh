package options

import (
	"github.com/projectdiscovery/goflags"
	"github.com/projectdiscovery/interactsh/pkg/server"
)

type CLIServerOptions struct {
	Resolvers                     goflags.StringSlice
	Config                        string
	Version                       bool
	Debug                         bool
	Domains                       goflags.StringSlice
	DnsTTL                        int
	DnsSubdomainRecords           goflags.StringSlice
	DnsPort                       int
	IPAddress                     string
	IPv6Address                   string
	ListenIP                      string
	HttpPort                      int
	HttpsPort                     int
	Hostmasters                   []string
	LdapWithFullLogger            bool
	Eviction                      int
	NoEviction                    bool
	Responder                     bool
	Smb                           bool
	SmbPort                       int
	SmtpPort                      int
	SmtpsPort                     int
	SmtpAutoTLSPort               int
	FtpPort                       int
	FtpsPort                      int
	LdapPort                      int
	Ftp                           bool
	Auth                          bool
	HTTPIndex                     string
	HTTPDirectory                 string
	HTTPReverseParams             goflags.StringSlice
	HTTPReverseProxy              string
	HTTPReverseInsecureSkipVerify bool
	Token                         string
	OriginURL                     string
	RootTLD                       bool
	FTPDirectory                  string
	SkipAcme                      bool
	DynamicResp                   bool
	CorrelationIdLength           int
	CorrelationIdNonceLength      int
	ScanEverywhere                bool
	CertificatePath               string
	CustomRecords                 string
	PrivateKeyPath                string
	OriginIPHeader                string
	DiskStorage                   bool
	DiskStoragePath               string
	EnablePprof                   bool
	EnableMetrics                 bool
	Verbose                       bool
	DisableUpdateCheck            bool
	NoVersionHeader               bool
	RealIPFrom                    goflags.StringSlice
	OriginIPEDNSopt               int
	HeaderServer                  string
}

func (cliServerOptions *CLIServerOptions) AsServerOptions() *server.Options {
	return &server.Options{
		Domains:                       cliServerOptions.Domains,
		DnsPort:                       cliServerOptions.DnsPort,
		DnsTTL:                        cliServerOptions.DnsTTL,
		DnsSubdomainRecords:           cliServerOptions.DnsSubdomainRecords,
		IPAddress:                     cliServerOptions.IPAddress,
		IPv6Address:                   cliServerOptions.IPv6Address,
		ListenIP:                      cliServerOptions.ListenIP,
		HttpPort:                      cliServerOptions.HttpPort,
		HttpsPort:                     cliServerOptions.HttpsPort,
		Hostmasters:                   cliServerOptions.Hostmasters,
		SmbPort:                       cliServerOptions.SmbPort,
		SmtpPort:                      cliServerOptions.SmtpPort,
		SmtpsPort:                     cliServerOptions.SmtpsPort,
		SmtpAutoTLSPort:               cliServerOptions.SmtpAutoTLSPort,
		FtpPort:                       cliServerOptions.FtpPort,
		FtpsPort:                      cliServerOptions.FtpsPort,
		LdapPort:                      cliServerOptions.LdapPort,
		Auth:                          cliServerOptions.Auth,
		HTTPIndex:                     cliServerOptions.HTTPIndex,
		HTTPDirectory:                 cliServerOptions.HTTPDirectory,
		HTTPReverseParams:             cliServerOptions.HTTPReverseParams,
		HTTPReverseProxy:              cliServerOptions.HTTPReverseProxy,
		HTTPReverseInsecureSkipVerify: cliServerOptions.HTTPReverseInsecureSkipVerify,
		Token:                         cliServerOptions.Token,
		Version:                       Version,
		DynamicResp:                   cliServerOptions.DynamicResp,
		OriginURL:                     cliServerOptions.OriginURL,
		RootTLD:                       cliServerOptions.RootTLD,
		FTPDirectory:                  cliServerOptions.FTPDirectory,
		CorrelationIdLength:           cliServerOptions.CorrelationIdLength,
		CorrelationIdNonceLength:      cliServerOptions.CorrelationIdNonceLength,
		ScanEverywhere:                cliServerOptions.ScanEverywhere,
		CertificatePath:               cliServerOptions.CertificatePath,
		CustomRecords:                 cliServerOptions.CustomRecords,
		PrivateKeyPath:                cliServerOptions.PrivateKeyPath,
		OriginIPHeader:                cliServerOptions.OriginIPHeader,
		DiskStorage:                   cliServerOptions.DiskStorage,
		DiskStoragePath:               cliServerOptions.DiskStoragePath,
		EnableMetrics:                 cliServerOptions.EnableMetrics,
		NoVersionHeader:               cliServerOptions.NoVersionHeader,
		HeaderServer:                  cliServerOptions.HeaderServer,
		RealIPFrom:                    cliServerOptions.RealIPFrom,
		OriginIPEDNSopt:               cliServerOptions.OriginIPEDNSopt,
	}
}
