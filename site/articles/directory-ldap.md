---
title: Company directory from LDAP and Active Directory
tile: Read your directory, turn people into extensions and groups into ring groups, and let staff sign in with their directory password.
icon: users
area: Operations
description: BitVoice reads Active Directory or LDAP with its own LDAPv3 client: people become extensions, groups become ring groups, and staff sign in by bind.
manual: ldap
---

## What LDAP and Active Directory are

Almost every organisation already keeps a list of its people somewhere central: their names, email addresses, phone numbers, and which teams they belong to. For most companies that list lives in a directory server, and the protocol used to read it is LDAP, the Lightweight Directory Access Protocol. Microsoft's Active Directory is the best-known directory; OpenLDAP, Samba, 389 Directory Server and FreeIPA are others, and they all speak LDAP.

A directory is a tree of entries, each with a distinguished name (DN) that gives its place in the tree, such as `cn=Jane Smith,ou=Staff,dc=example,dc=com`. Each entry has attributes: `displayName`, `mail`, `telephoneNumber`, and on Active Directory `sAMAccountName` (the short login name) and `memberOf` (the groups the person belongs to). LDAP has a handful of operations. Bind authenticates a connection, either as a reading account or as a person checking their own password. Search returns entries that match a filter, such as "everyone in Staff who has a phone number." Because a search can return thousands of entries, the protocol has paging: the server sends a page, a cookie to ask for the next, and so on until the cookie is empty.

Here is the point of connecting a phone system to it. If the directory already knows that Jane Smith is in Sales, has the number +1 212 555 0123 and the login `jsmith`, then the phone system should not need any of that typed in again. Jane should become extension 0123, Sales should become a ring group of the right people, and Jane should sign in to the phone system's web interface with the password she already uses, so that when she leaves and her directory account is disabled, her access to the phone system goes with it.

## Where it usually goes wrong

The failures are familiar to anyone who has wired a system to Active Directory. Plain LDAP sends the bind password in clear text, so a sync set up quickly over `ldap://` leaks the reading account's password, and worse, every person's password when sign-in by bind is used, to anyone on the network. Paging is the next trap: Active Directory returns at most 1,000 entries per search by default, so a sync that ignores paging silently reads only the first thousand people and nobody notices until the thousand-and-first calls.

Then there is ownership. A sync that overwrites everything each time wipes out the local settings an administrator carefully made; one that never updates anything leaves the directory and the phone system drifting apart. Disabled accounts are a security hole if they keep working; nested groups (a group that contains a group) are missed by naive code, so half a team never lands in the ring group. And an empty password, which LDAP treats as a request for an anonymous bind, will succeed against many servers, so a sign-in form that passes an empty password straight through lets anyone in as anyone.

## How BitVoice does it

BitVoice has its own LDAPv3 client and reads the directory; it never writes to it. Connect it in **Settings → Company directory**. Choose **Active Directory** or **OpenLDAP or another LDAP server**, which sets which attributes are read (`sAMAccountName` or `uid` for the sign-in name, `displayName` or `cn`, `mail`, `telephoneNumber`), and change any of them in its box. The **Server** is `ldaps://dc1.example.com` on port 636, or `ldap://...` with **StartTLS** on. Plain LDAP is refused unless the server is this computer, because the passwords would cross the network unencrypted; when the server's certificate comes from your own authority, paste that authority in **Company certificate authority**. Give a **Reading account** and its password, which may be a key-vault reference, and the **Base DN** where people and groups live. **Which people** narrows them with an LDAP filter, and on Active Directory disabled accounts are always left out. **Test** signs in with the reading account and reads everyone, changing nothing: it shows how many people it found, how many have a number, and the extension each would get.

For extensions, with **Make and update extensions** on, each person with a number gets one. The number's last **Digits kept** become the extension (four of `+1 212 555 0123` is 0123; zero keeps every digit, which must then be two to eight). New extensions get a SIP password and voicemail with a random PIN; the name and email come from the directory. The directory owns an extension it made, so its number, name and email follow the directory at each sync, while everything else (devices, forwarding, voicemail settings) stays yours. An extension you made by hand is never taken over unless it already carries that person's name or email. Clashes, two people with the same number, or a number of the wrong length, are listed under **To look at** and nothing is changed for them. With **Turn off the extensions of people who left** on, an extension the directory made is switched off when the person leaves, is disabled, or loses their number, and comes back when they return.

For groups, **Ring groups filled from directory groups** puts the extensions of a group's members into a ring group, and on Active Directory nested groups count. The directory is read every **Read it every** minutes, sixty by default, and at once with **Sync now**. Each sync that changes something is one [revision](/manual/#changes) named "Company directory sync", so it can be undone, and the last sync's report shows below the button.

For sign-in, with **People sign in here with their directory name and password** on, the sign-in page takes directory names and passwords beside BitVoice's own accounts. Members of **Administrator groups** sign in as administrators, of **Viewer groups** as viewers, named by their `cn` or DN, with nested groups counted on Active Directory; someone in no such group is refused. The account here is made at the first sign-in and its role follows the groups at every sign-in, and it never takes over a BitVoice account of the same name. An empty password is always refused, so LDAP cannot turn it into an anonymous bind; a disabled account cannot sign in; and names are escaped before they reach the directory, so a crafted username cannot change the search filter. Two-step sign-in can be added to these accounts as to any other.

## Why BitVoice fits

The LDAPv3 client is BitVoice's own, in the one Go program, so there is no separate connector service to run or keep in step. Everything is in the web interface: the server, the attribute mapping, the filter, the digit rule, the group-to-ring-group lines and the sign-in groups, with a **Test** that reads the whole directory and shows the outcome before you commit. StartTLS and LDAPS are both supported and plain LDAP off the local machine is refused, so the common mistake of leaking passwords is made hard. Because a sync is a revision, a mistaken rule is undone in one click. And it is Apache-2.0 and free, with directory integration in the box rather than behind a paid tier.

## What has been tested, and what has not

The maturity table rates the company directory **Checked against a reference**, meaning tested against independent implementations rather than only BitVoice's own tests. It has been run against OpenLDAP 2.4 with StartTLS and LDAPS, reading 1,203 people through paging past OpenLDAP's 1,000-entry limit, reading groups and signing in; and against Samba 4's Active Directory over LDAPS, again paging past 1,000, leaving disabled accounts out, and reading nested groups for ring groups and roles. It has not yet been tried against Microsoft's own Windows Server domain controller, which is the honest limit to keep in mind, though Samba 4 implements the same Active Directory protocol. The reading account's password can be held in a key vault; AWS Secrets Manager has been read from real AWS, while Azure Key Vault is so far tested against a stand-in.

## Read more

- [The manual: Company directory (Active Directory and LDAP)](/manual/#ldap)
- [Security](/technology/security/)
- [Running BitVoice: one program, installers and updates](/technology/operations/)
