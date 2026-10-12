package api

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// imapClient is just enough IMAP4rev1 (RFC 3501) to read a mailbox: sign in, open a folder, find
// unread messages, fetch each whole, and mark it read.
type imapClient struct {
	conn net.Conn
	r    *bufio.Reader
	tag  int
}

// dialIMAP connects over TLS (port 993) and reads the greeting. dial may be nil.
func dialIMAP(host string, port int, dial func(addr string) (net.Conn, error)) (*imapClient, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var conn net.Conn
	var err error
	if dial != nil {
		conn, err = dial(addr)
	} else {
		d := &net.Dialer{Timeout: 15 * time.Second}
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	if err != nil {
		return nil, fmt.Errorf("could not reach the mail server: %w", err)
	}
	c := &imapClient{conn: conn, r: bufio.NewReaderSize(conn, 64<<10)}
	c.deadline()
	line, err := c.line()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.HasPrefix(line, "* OK") && !strings.HasPrefix(line, "* PREAUTH") {
		conn.Close()
		return nil, errors.New("the mail server did not greet as an IMAP server")
	}
	return c, nil
}

func (c *imapClient) deadline() { _ = c.conn.SetDeadline(time.Now().Add(2 * time.Minute)) }

func (c *imapClient) Close() error { return c.conn.Close() }

func (c *imapClient) line() (string, error) {
	s, err := c.r.ReadString('\n')
	if err != nil {
		return "", errors.New("the mail server closed the connection")
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// quote makes an IMAP quoted string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// response is one untagged line, with any literal ({n} and n octets) it carried.
type response struct {
	text     string
	literals [][]byte
}

// cmd sends a command and reads until its tagged reply, returning the untagged responses.
func (c *imapClient) cmd(format string, args ...any) ([]response, error) {
	c.tag++
	tag := "b" + strconv.Itoa(c.tag)
	c.deadline()
	if _, err := fmt.Fprintf(c.conn, tag+" "+format+"\r\n", args...); err != nil {
		return nil, errors.New("the mail server closed the connection")
	}
	var out []response
	for {
		line, err := c.line()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, tag+" ") {
			status := strings.TrimPrefix(line, tag+" ")
			if strings.HasPrefix(status, "OK") {
				return out, nil
			}
			return out, errors.New("the mail server said: " + strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(status, "NO"), "BAD")))
		}
		resp := response{text: line}
		// A line ending in {n} is followed by n octets, then the rest of the line.
		for {
			i := strings.LastIndex(line, "{")
			if i < 0 || !strings.HasSuffix(line, "}") {
				break
			}
			n, err := strconv.Atoi(line[i+1 : len(line)-1])
			if err != nil || n < 0 || n > 50<<20 {
				return nil, errors.New("the mail server sent something too large")
			}
			lit := make([]byte, n)
			if _, err := io.ReadFull(c.r, lit); err != nil {
				return nil, errors.New("the mail server closed the connection")
			}
			resp.literals = append(resp.literals, lit)
			if line, err = c.line(); err != nil {
				return nil, err
			}
			resp.text += " " + line
		}
		out = append(out, resp)
	}
}

func (c *imapClient) Login(user, pass string) error {
	if _, err := c.cmd("LOGIN %s %s", quote(user), quote(pass)); err != nil {
		return errors.New("the mailbox refused the sign-in: check the username and password")
	}
	return nil
}

func (c *imapClient) Select(folder string) error {
	if folder == "" {
		folder = "INBOX"
	}
	if _, err := c.cmd("SELECT %s", quote(folder)); err != nil {
		return fmt.Errorf("the folder %q could not be opened: %w", folder, err)
	}
	return nil
}

// Unseen is the UIDs of the unread messages.
func (c *imapClient) Unseen() ([]uint32, error) {
	rs, err := c.cmd("UID SEARCH UNSEEN")
	if err != nil {
		return nil, err
	}
	var ids []uint32
	for _, r := range rs {
		if !strings.HasPrefix(r.text, "* SEARCH") {
			continue
		}
		for _, f := range strings.Fields(strings.TrimPrefix(r.text, "* SEARCH")) {
			if n, err := strconv.ParseUint(f, 10, 32); err == nil {
				ids = append(ids, uint32(n))
			}
		}
	}
	return ids, nil
}

// Fetch is one whole message, left unread.
func (c *imapClient) Fetch(uid uint32) ([]byte, error) {
	rs, err := c.cmd("UID FETCH %d BODY.PEEK[]", uid)
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if strings.Contains(r.text, "FETCH") && len(r.literals) > 0 {
			return r.literals[0], nil
		}
	}
	return nil, errors.New("the message was not there")
}

// Seen marks a message read.
func (c *imapClient) Seen(uid uint32) error {
	_, err := c.cmd(`UID STORE %d +FLAGS.SILENT (\Seen)`, uid)
	return err
}

func (c *imapClient) Logout() {
	_, _ = c.cmd("LOGOUT")
	c.Close()
}
