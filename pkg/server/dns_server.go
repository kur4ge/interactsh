package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/interactsh/pkg/server/acme"
	stringsutil "github.com/projectdiscovery/utils/strings"
	"gopkg.in/yaml.v3"
)

var (
	HEX_IP_REGEX = regexp.MustCompile("^[a-f0-9]{8}$")
)

// DNSServer is a DNS server instance that listens on port 53.
type DNSServer struct {
	options       *Options
	mxDomains     map[string]string
	nsDomains     map[string][]string
	ipAddress     net.IP
	ipv6Address   net.IP
	timeToLive    uint32
	server        *dns.Server
	customRecords *customDNSRecords
	TxtRecord     string // used for ACME verification
}

// NewDNSServer returns a new DNS server.
func NewDNSServer(network string, options *Options) *DNSServer {
	mxDomains := make(map[string]string)
	nsDomains := make(map[string][]string)

	for _, domain := range options.Domains {
		dotdomain := dns.Fqdn(domain)

		mxDomain := fmt.Sprintf("mail.%s", dotdomain)
		mxDomains[dotdomain] = mxDomain

		ns1Domain := fmt.Sprintf("ns1.%s", dotdomain)
		ns2Domain := fmt.Sprintf("ns2.%s", dotdomain)
		nsDomains[dotdomain] = []string{ns1Domain, ns2Domain}
	}

	server := &DNSServer{
		options:       options,
		ipAddress:     net.ParseIP(options.IPAddress),
		ipv6Address:   net.ParseIP(options.IPv6Address),
		mxDomains:     mxDomains,
		nsDomains:     nsDomains,
		timeToLive:    uint32(options.DnsTTL),
		customRecords: newCustomDNSRecordsServer(options),
	}
	server.server = &dns.Server{
		Addr:    options.ListenIP + fmt.Sprintf(":%d", options.DnsPort),
		Net:     network,
		Handler: server,
	}
	return server
}

// ListenAndServe listens on dns ports for the server.
func (h *DNSServer) ListenAndServe(dnsAlive chan bool) {
	dnsAlive <- true
	if err := h.server.ListenAndServe(); err != nil {
		gologger.Error().Msgf("Could not listen for %s DNS on %s (%s)\n", strings.ToUpper(h.server.Net), h.server.Addr, err)
		dnsAlive <- false
	}
}

// ServeDNS is the default handler for DNS queries.
func (h *DNSServer) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	atomic.AddUint64(&h.options.Stats.Dns, 1)

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	// bail early for no queries.
	if len(r.Question) == 0 {
		return
	}

	isDNSChallenge := false
	for _, question := range r.Question {
		domain := question.Name

		// Handle DNS server cases for ACME server
		if strings.HasPrefix(strings.ToLower(domain), acme.DNSChallengeString) {
			isDNSChallenge = true

			gologger.Debug().Msgf("Got acme dns request: \n%s\n", r.String())

			switch question.Qtype {
			case dns.TypeSOA:
				h.handleSOA(domain, m)
			case dns.TypeTXT:
				err := h.handleACMETXTChallenge(domain, m)
				if err != nil {
					fmt.Printf("handleACMETXTChallenge for zone %s err: %+v\n", domain, err)
					return
				}
			case dns.TypeNS:
				h.handleNS(domain, m)
			case dns.TypeA:
				h.handleACNAMEANY(domain, m)
			case dns.TypeAAAA:
				h.handleAAAACNAMEANY(domain, m)
			}

			gologger.Debug().Msgf("Got acme dns response: \n%s\n", m.String())
		} else {
			switch question.Qtype {
			case dns.TypeA, dns.TypeCNAME, dns.TypeANY:
				h.handleACNAMEANY(domain, m)
			case dns.TypeAAAA:
				h.handleAAAACNAMEANY(domain, m)
			case dns.TypeMX:
				h.handleMX(domain, m)
			case dns.TypeNS:
				h.handleNS(domain, m)
			case dns.TypeSOA:
				h.handleSOA(domain, m)
			case dns.TypeTXT:
				h.handleTXT(domain, m)
			}
		}
	}
	if !isDNSChallenge {
		// Write interaction for first question and dns request
		h.handleInteraction(r.Question[0].Name, w, r, m)
	}

	if err := w.WriteMsg(m); err != nil {
		gologger.Warning().Msgf("Could not write DNS response: \n%s\n %s\n", m.String(), err)
	}
}

// handleACMETXTChallenge handles solving of ACME TXT challenge with the given provider
func (h *DNSServer) handleACMETXTChallenge(zone string, m *dns.Msg) error {
	records, err := h.options.ACMEStore.GetRecords(context.Background(), strings.ToLower(zone))
	if err != nil {
		return err
	}

	rrs := []dns.RR{}
	for _, record := range records {
		txtHdr := dns.RR_Header{Name: zone, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: uint32(record.TTL)}
		rrs = append(rrs, &dns.TXT{Hdr: txtHdr, Txt: []string{record.Value}})
	}
	m.Answer = append(m.Answer, rrs...)
	return nil
}

// handleACNAMEANY handles A, CNAME or ANY queries for DNS server
func (h *DNSServer) handleACNAMEANY(zone string, m *dns.Msg) {
	nsHeader := dns.RR_Header{Name: zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: h.timeToLive}

	// If we have a custom record serve it, or default IP
	record := h.customRecords.checkCustomResponse(zone)
	switch {
	case record != "":
		h.resultFunction(nsHeader, zone, net.ParseIP(record), m)
	default:
		h.resultFunction(nsHeader, zone, h.ipAddress, m)
	}
}

// handleAAAACNAMEANY handles AAAA queries for DNS server
func (h *DNSServer) handleAAAACNAMEANY(zone string, m *dns.Msg) {
	nsHeader := dns.RR_Header{Name: zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: h.timeToLive}

	// If we have a custom record serve it, or default IPv6
	record := h.customRecords.checkCustomAAAAResponse(zone)
	switch {
	case record != "":
		h.resultFunctionAAAA(nsHeader, zone, net.ParseIP(record), m)
	default:
		h.resultFunctionAAAA(nsHeader, zone, h.ipv6Address, m)
	}
}

func (h *DNSServer) resultFunction(nsHeader dns.RR_Header, zone string, ipAddress net.IP, m *dns.Msg) {
	m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: zone, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: h.timeToLive}, A: ipAddress})
	dotDomains := []string{zone, dns.Fqdn(h.options.Domains[0])}
	for _, dotDomain := range dotDomains {
		if nsDomains, ok := h.nsDomains[dotDomain]; ok {
			for _, nsDomain := range nsDomains {
				m.Ns = append(m.Ns, &dns.NS{Hdr: nsHeader, Ns: nsDomain})
				m.Extra = append(m.Extra, &dns.A{Hdr: dns.RR_Header{Name: nsDomain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: h.timeToLive}, A: h.ipAddress})
			}
			return
		}
	}
}

func (h *DNSServer) resultFunctionAAAA(nsHeader dns.RR_Header, zone string, ipAddress net.IP, m *dns.Msg) {
	m.Answer = append(m.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: zone, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: h.timeToLive}, AAAA: ipAddress})
	dotDomains := []string{zone, dns.Fqdn(h.options.Domains[0])}
	for _, dotDomain := range dotDomains {
		if nsDomains, ok := h.nsDomains[dotDomain]; ok {
			for _, nsDomain := range nsDomains {
				m.Ns = append(m.Ns, &dns.NS{Hdr: nsHeader, Ns: nsDomain})
				m.Extra = append(m.Extra, &dns.A{Hdr: dns.RR_Header{Name: nsDomain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: h.timeToLive}, A: h.ipAddress})
			}
			return
		}
	}
}

func (h *DNSServer) handleMX(zone string, m *dns.Msg) {
	nsHdr := dns.RR_Header{Name: zone, Rrtype: dns.TypeMX, Class: dns.ClassINET, Ttl: h.timeToLive}

	dotDomains := []string{zone, dns.Fqdn(h.options.Domains[0])}
	for _, dotDomain := range dotDomains {
		if mxdomain, ok := h.mxDomains[dotDomain]; ok {
			m.Answer = append(m.Answer, &dns.MX{Hdr: nsHdr, Mx: mxdomain, Preference: 1})
			return
		}
	}
}

func (h *DNSServer) handleNS(zone string, m *dns.Msg) {
	nsHeader := dns.RR_Header{Name: zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: h.timeToLive}

	dotDomains := []string{zone, dns.Fqdn(h.options.Domains[0])}
	for _, dotDomain := range dotDomains {
		if nsDomains, ok := h.nsDomains[dotDomain]; ok {
			for _, nsDomain := range nsDomains {
				m.Answer = append(m.Answer, &dns.NS{Hdr: nsHeader, Ns: nsDomain})
			}
			return
		}
	}
}

func (h *DNSServer) handleSOA(zone string, m *dns.Msg) {
	nsHdr := dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET}
	dotDomains := []string{zone, dns.Fqdn(h.options.Domains[0])}
	for _, dotDomain := range dotDomains {
		if nsDomains, ok := h.nsDomains[dotDomain]; ok {
			for _, nsDomain := range nsDomains {
				m.Answer = append(m.Answer, &dns.SOA{Hdr: nsHdr, Ns: nsDomain, Mbox: acme.CertificateAuthority, Serial: 1, Expire: 60, Minttl: 60})
				return
			}
		}
	}
}

func (h *DNSServer) handleTXT(zone string, m *dns.Msg) {
	m.Answer = append(m.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: zone, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}, Txt: []string{h.TxtRecord}})
}

func toQType(ttype uint16) (rtype string) {
	switch ttype {
	case dns.TypeA:
		rtype = "A"
	case dns.TypeNS:
		rtype = "NS"
	case dns.TypeCNAME:
		rtype = "CNAME"
	case dns.TypeSOA:
		rtype = "SOA"
	case dns.TypePTR:
		rtype = "PTR"
	case dns.TypeMX:
		rtype = "MX"
	case dns.TypeTXT:
		rtype = "TXT"
	case dns.TypeAAAA:
		rtype = "AAAA"
	}
	return
}

// handleInteraction handles an interaction for the DNS server
func (h *DNSServer) handleInteraction(domain string, w dns.ResponseWriter, r *dns.Msg, m *dns.Msg) {
	var uniqueID, fullID string

	requestMsg := r.String()
	responseMsg := m.String()

	gologger.Debug().Msgf("New DNS request: %s\n", requestMsg)

	var foundDomain string
	for _, configuredDomain := range h.options.Domains {
		configuredDotDomain := dns.Fqdn(configuredDomain)
		if stringsutil.HasSuffixI(domain, configuredDotDomain) {
			foundDomain = configuredDomain
			break
		}
	}

	// if root-tld is enabled stores any interaction towards the main domain
	if h.options.RootTLD && foundDomain != "" {
		correlationID := foundDomain
		host := h.getMsgHost(w, r)
		interaction := &Interaction{
			Protocol:      "dns",
			UniqueID:      domain,
			FullId:        domain,
			QType:         toQType(r.Question[0].Qtype),
			RawRequest:    requestMsg,
			RawResponse:   responseMsg,
			RemoteAddress: host,
			Timestamp:     time.Now(),
		}

		if nil != h.options.OnResult {
			h.options.OnResult(interaction)
		}

		buffer := &bytes.Buffer{}
		if err := jsoniter.NewEncoder(buffer).Encode(interaction); err != nil {
			gologger.Warning().Msgf("Could not encode root tld dns interaction: %s\n", err)
		} else {
			gologger.Debug().Msgf("Root TLD DNS Interaction: \n%s\n", buffer.String())
			if err := h.options.Storage.AddInteractionWithId(correlationID, buffer.Bytes()); err != nil {
				gologger.Warning().Msgf("Could not store dns interaction: %s\n", err)
			}
		}
	}

	if foundDomain != "" {
		if h.options.ScanEverywhere {
			chunks := stringsutil.SplitAny(requestMsg, ".\n\t\"'")
			for _, chunk := range chunks {
				for part := range stringsutil.SlideWithLength(chunk, h.options.GetIdLength()) {
					normalizedPart := strings.ToLower(part)
					if h.options.isCorrelationID(normalizedPart) {
						uniqueID = normalizedPart
						fullID = part
					}
				}
			}
		} else {
			parts := strings.Split(domain, ".")
			for i, part := range parts {
				subParts := splitSubdomainParts(part)
				for _, sub := range subParts {
					if h.options.isCorrelationID(sub) {
						uniqueID = sub
						fullID = part
						if i+1 <= len(parts) {
							fullID = strings.Join(parts[:i+1], ".")
						}
					}
				}
			}
		}
	}

	if uniqueID != "" {
		correlationID := h.options.getCorrelationID(uniqueID)
		host := h.getMsgHost(w, r)
		interaction := &Interaction{
			Protocol:      "dns",
			UniqueID:      uniqueID,
			FullId:        fullID,
			QType:         toQType(r.Question[0].Qtype),
			RawRequest:    requestMsg,
			RawResponse:   responseMsg,
			RemoteAddress: host,
			Timestamp:     time.Now(),
		}
		buffer := &bytes.Buffer{}
		if err := jsoniter.NewEncoder(buffer).Encode(interaction); err != nil {
			gologger.Warning().Msgf("Could not encode dns interaction: %s\n", err)
		} else {
			gologger.Debug().Msgf("DNS Interaction: \n%s\n", buffer.String())
			if err := h.options.Storage.AddInteraction(correlationID, buffer.Bytes()); err != nil {
				gologger.Warning().Msgf("Could not store dns interaction: %s\n", err)
			}
		}
	}
}

func (h *DNSServer) getMsgHost(w dns.ResponseWriter, r *dns.Msg) string {
	host, _, _ := net.SplitHostPort(w.RemoteAddr().String())
	if h.options.OriginIPEDNSopt < 0 {
		return host
	}

	isTrusted := false
	checkIP := net.ParseIP(host)

	for _, test := range h.options.RealIPFrom {
		if strings.Contains(test, "/") {
			_, cidr, err := net.ParseCIDR(test)
			if err != nil {
				gologger.Error().Msgf("Invalid CIDR format: %s, err: %s", test, err)
			}
			if cidr.Contains(checkIP) {
				isTrusted = true
				break
			}
		} else {
			ip := net.ParseIP(test)
			if ip == nil {
				gologger.Error().Msgf("Invalid IP address: %s", test)
			}
			if ip.Equal(checkIP) {
				isTrusted = true
				break
			}
		}
	}

	if !isTrusted {
		return host
	}

	for _, extra := range r.Extra {
		switch rr := extra.(type) {
		case *dns.OPT:
			for _, option := range rr.Option {
				switch opt := option.(type) {
				case *dns.EDNS0_LOCAL:
					if opt.Code == uint16(h.options.OriginIPEDNSopt) {
						ip := net.IP(opt.Data)
						testHost := ip.String()
						if net.ParseIP(testHost) == nil {
							gologger.Warning().Msgf("Invalid origin IP address: %s\n", opt.String())
							return host
						}
						return testHost
					}
				}
			}
		}
	}

	return host
}

// customDNSRecords is a server for custom dns records
type customDNSRecords struct {
	records            map[string]string
	v6Records          map[string]string
	subdomainRecords   map[string]string
	subdomainV6Records map[string]string
}

// defaultCustomRecords is the list of default custom DNS records
var defaultCustomRecords = map[string]string{
	"aws":       "169.254.169.254",
	"alibaba":   "100.100.100.200",
	"localhost": "127.0.0.1",
	"oracle":    "192.0.0.192",
}

// defaultCustomV6Records is the list of default custom DNS records
var defaultCustomV6Records = map[string]string{
	"localhost": "::1",
}

func newCustomDNSRecordsServer(options *Options) *customDNSRecords {
	subdomainRecords := make(map[string]string)
	subdomainV6Records := make(map[string]string)
	for _, m := range options.DnsSubdomainRecords {
		parts := strings.SplitN(m, "=", 2)
		if len(parts) == 2 {
			ip := net.ParseIP(parts[1])
			if ip == nil {
				gologger.Warning().Msgf("Invalid DnsSubdomainRecord: %s, err: Invalid IP address.", m)
			} else {
				if ip.To4() != nil {
					subdomainRecords[strings.ToLower(parts[0])] = parts[1]
				} else {
					subdomainV6Records[strings.ToLower(parts[0])] = parts[1]
				}
			}
		}
	}

	server := &customDNSRecords{
		records:            make(map[string]string),
		v6Records:          make(map[string]string),
		subdomainRecords:   subdomainRecords,
		subdomainV6Records: subdomainV6Records,
	}

	input := options.CustomRecords
	for k, v := range defaultCustomRecords {
		server.records[k] = v
	}
	for k, v := range defaultCustomV6Records {
		server.v6Records[k] = v
	}

	if input != "" {
		if err := server.readRecordsFromFile(input); err != nil {
			gologger.Error().Msgf("Could not read custom DNS records: %s", err)
		}
	}
	return server
}

type customRecordConfig struct {
	IPv4 map[string]string `yaml:"ipv4"`
	IPv6 map[string]string `yaml:"ipv6"`
}

func (c *customDNSRecords) readRecordsFromFile(input string) error {
	file, err := os.Open(input)
	if err != nil {
		return errors.Wrap(err, "could not open file")
	}
	defer file.Close()

	var data customRecordConfig

	if err := yaml.NewDecoder(file).Decode(&data); err != nil {
		return errors.Wrap(err, "could not decode file")
	}
	for k, v := range data.IPv4 {
		c.records[strings.ToLower(k)] = v
	}
	for k, v := range data.IPv6 {
		c.v6Records[strings.ToLower(k)] = v
	}

	return nil
}

func (c *customDNSRecords) checkCustomResponse(zone string) string {
	parts := strings.SplitN(zone, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	if value, ok := c.records[strings.ToLower(parts[0])]; ok {
		return value
	}

	subParts := splitSubdomainParts(parts[0])
	if len(subParts) == 1 {
		return ""
	}
	ips := make([]string, 0)
	for _, part := range subParts {
		if part == "" {
			ips = append(ips, "") // "" represent options.IPAddress
		} else if ok := HEX_IP_REGEX.MatchString(part); ok {
			ip, err := hex.DecodeString(part)
			if err != nil {
				continue
			}
			ips = append(ips, net.IP(ip).String())
		} else if ans, ok := c.subdomainRecords[strings.ToLower(part)]; ok {
			ips = append(ips, ans)
		}
	}
	if len(ips) == 0 {
		return ""
	}
	return ips[rand.Intn(len(ips))]
}

// only return IPv6
func (c *customDNSRecords) checkCustomAAAAResponse(zone string) string {
	parts := strings.SplitN(zone, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	if value, ok := c.v6Records[strings.ToLower(parts[0])]; ok {
		return value
	}

	subParts := splitSubdomainParts(parts[0])
	if len(subParts) == 1 {
		return ""
	}

	ips := make([]string, 0)
	for _, part := range subParts {
		if part == "" {
			ips = append(ips, "") // "" represent options.IPv6Address
		} else if ans, ok := c.subdomainV6Records[strings.ToLower(part)]; ok {
			ips = append(ips, ans)
		}
	}
	if len(ips) == 0 {
		return ""
	}
	return ips[rand.Intn(len(ips))]
}

func splitSubdomainParts(s string) []string {
	var r []string
	p := ""
	for _, c := range s {
		if c == '-' || c == '_' {
			r, p = append(r, p), ""
		} else {
			p = p + string(c)
		}
	}
	r = append(r, p)
	return r
}
