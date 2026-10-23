Human use cases for Deskyard: families and small companies centrally running desktops for their children or teams. One person manages the environments; each person opens a familiar, persistent desktop in a browser. Agents can help within those environments, but these use cases do not depend on them.

# Direction and scope

The family use case is an immediate need for the project owner, stated on 2026-10-23. Explore small-company use cases alongside it to identify shared needs and differences. The scenarios below are working hypotheses for discussion, not an approved feature specification or a claim that every workflow is supported today.

The common unit is a desktop with its own persistent workspace and explicit access. A personal desktop belongs to one person's daily work; a shared desktop serves a purpose and may have several people using the same screen. These are different usage patterns even when they use the same underlying machinery.

# Families

The administrator is a parent or other trusted household member. The everyday users are children and potentially other family members. The administrator wants to maintain a manageable set of environments; each person wants their computer to be ready when they need it.

## F1: A personal desktop for each child

A parent prepares an environment with the applications a child needs, creates a separate desktop and grants the child access. The child signs in from a household device and returns to their own files, browser profile and desktop state. Siblings use separate desktops so their work and sign-ins do not get mixed together.

Success means the child can reach their desktop without administering Linux, installing the base environment or understanding containers. Changing the device used to connect should not require setting up their environment again. Which devices, input methods and applications must work is still to be established.

## F2: Prepare and maintain learning environments

A parent prepares applications and initial settings for schoolwork, creative projects or programming, then reuses that setup for more than one child. Later, the parent updates the environment while preserving each child's personal work.

Success means less repeated setup and a clear distinction between the common environment and each child's files and customizations. An image update, a reset and recovery of deleted work are separate operations; they should not be treated as interchangeable.

## F3: Help a child in the same desktop

A child gets stuck and a parent opens the child's desktop to help with the task on the same screen. Both can see the result without moving files to another machine or reconstructing the problem.

Success means both people understand that the screen, keyboard, mouse and signed-in applications are shared. How help is requested, how another viewer is announced and whether joining requires consent remain product questions. Existing administrator access should not be mistaken for a designed consent workflow.

## F4: Recover from mistakes and device loss

A child deletes a project, breaks their setup or loses access to the device they normally use. The parent wants to get them working again and recover their work where possible.

Success means distinguishing a lost client device from lost server data, and restoring the right files or environment without erasing newer work. Persistent storage provides continuity across container replacement; it does not by itself provide backup or undo. Backup scope, restore granularity and recovery procedures need further exploration.

## F5: Manage internet access outside the child's desktop

A parent wants to choose the DNS resolver used by children's desktops, use a filtering service such as Pi-hole or AdGuard Home, and independently route a desktop's internet traffic through a VPN. These controls should be managed outside the child's environment and should survive restarts and image upgrades. This need was raised on 2026-10-23.

Confirmed requirements from the project owner:

- Configure custom DNS per desktop, with an administrator-controlled way to enable or disable it for that desktop.
- Independently enable or disable VPN routing per desktop. When enabled, all of that desktop's internet traffic must use the VPN, not only its browser traffic. NordVPN is a concrete provider of interest; provider integration and credentials have not yet been designed or verified.
- Enforce the selected policy outside the desktop. These are not merely default settings that the desktop user can change or bypass.
- The VPN scope is the ordinary desktop network path, not complete lockdown. Keep existing agent port forwarding unchanged; deliberate alternate tunnels or proxies through allowed applications or forwarding are outside the VPN feature's bypass-prevention scope.
- For VPN-enabled desktops, block access to the home network by default. Administrators can configure explicit exceptions; preserving Deskyard's access to the desktop must not grant the desktop general access to the host or LAN.
- Allow DNS filtering and VPN routing to be used separately or together. Selecting a VPN must not silently override an enabled custom DNS policy.
- Make setup as easy as possible through Deskyard's UI, without requiring the administrator to edit Docker networking, firewall rules or routing tables.

Agreed configuration flow:

- Set up named, reusable DNS services and VPN connections once, including the provider configuration and credentials needed for a connection such as NordVPN. The exact provider setup steps still need design and verification.
- On each desktop, independently choose "Default DNS" or a saved DNS service, and "No VPN" or a saved VPN connection.
- Show clear connection status such as connected, connecting or blocked, with understandable errors and guidance when configuration or connectivity fails.
- Keep routing and enforcement details behind these controls. Applying a saved configuration must enforce the policy, not merely change what the UI displays.

Still to decide: VPN/provider configuration format, the format and scope of administrator-configured LAN exceptions, the precise supervisor traffic required, behavior during DNS failure, how policy changes affect existing connections, and the supported scope of bypass prevention. These decisions belong in a feature design before implementation.

VPN design decisions confirmed during brainstorming: this is outbound routing from the desktop, not a VPN for connecting to Deskyard. Linux is the deployment target and macOS should support development tests. On VPN failure, block internet access, reconnect automatically and preserve desktop access through Deskyard; only an administrator disabling VPN restores direct access. An isolated OrbStack test with a real desktop passed tunnel traffic, failure blocking, recovery and local Caddy reachability; see [[superpowers/spikes/2026-10-23-desktop-vpn-macos|Desktop VPN on macOS spike]] for evidence and limitations. Provider setup, DNS failure behavior and the remaining enforcement details still need design.

Provider scope agreed during brainstorming: begin with guided NordVPN setup and country selection, and keep the underlying design extensible to other providers. The project owner has a NordVPN account available for live testing; credentials should be supplied locally, not recorded in these docs. Other providers are not a requirement for the first release.

The [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] records the proposed architecture, setup, network policy, lifecycle and tests. A desktop restart when enabling, disabling or changing VPN settings is acceptable; automatic tunnel reconnects should not restart the desktop. Custom DNS remains a separate design and implementation step after VPN.

DNS filtering and VPN routing serve different purposes. A filtering resolver can block domain lookups according to its policy; it does not inspect every page or guarantee safe content. A VPN changes the route and exit point for traffic; it does not inherently filter content. They can be combined, provided DNS follows the intended policy rather than silently switching to the VPN provider's resolver. See the [Pi-hole Docker documentation](https://docs.pi-hole.net/docker/), [AdGuard Home overview](https://adguard-dns.io/kb/adguard-home/overview/) and [DNS filtering limitations](https://github.com/AdguardTeam/AdGuardHome/blob/master/README.md#known-limitations).

The current implementation creates a separate Docker bridge network per desktop. Its engine interface has no explicit DNS-resolver or VPN-policy setting (`supervisor/internal/engine/engine.go`, `supervisor/internal/engine/docker/docker.go`, `supervisor/internal/desktops/service.go`). Docker supports daemon-wide DNS defaults and per-container DNS settings; on custom networks its embedded resolver forwards external lookups to configured upstream resolvers. An external Pi-hole or AdGuard Home instance is therefore a possible infrastructure integration, but Deskyard does not yet offer per-desktop selection or enforcement. The resolver must be reachable from the container networks; a loopback address refers to the container itself, not a separate DNS service. See [Docker networking and DNS](https://docs.docker.com/engine/network/#dns-services).

Routing selected desktop subnets through a VPN at an external gateway or the container host is another candidate integration. It needs explicit routing and firewall rules and validation on the deployment platform; installing a VPN on the host alone does not establish that container traffic uses it. A separate VPN container is also a possible design, but simply attaching it to a desktop's network does not make it the gateway. Sharing its network namespace would require changes to Deskyard's current networking and desktop reachability assumptions. Keep desktop isolation and supervisor access intact while evaluating these approaches. [Gluetun's firewall documentation](https://github.com/qdm12/gluetun-wiki/blob/main/faq/firewall.md) illustrates a VPN kill switch, not an integration already supported by Deskyard.

Success means the parent can understand and verify which DNS and exit route each desktop uses. Enforcement is required: test alternate DNS, browser DNS-over-HTTPS, application proxies or tunnels, IPv6 and direct connections, and define what happens when the resolver or VPN fails. A configured default is weaker than enforced network policy. Include any intended access to the home LAN and the effect of Deskyard's existing port-forwarding capabilities in that policy.

The small-company counterpart is administrator-managed DNS and egress: use an internal resolver, reach company resources through a VPN or choose an approved internet exit. The mechanism may be shared with families, while filtering rules, exceptions and privacy expectations differ.

## Family questions

- What is the first real household setup: number and ages of users, connecting devices, required applications and expected simultaneous use?
- Will the server be used only at home, or must children reach it elsewhere? What should happen when the server or network is unavailable?
- Is the aim convenient administration, or also enforced limits on time, applications and websites? These are distinct needs; an environment with selected apps is not automatically a parental-control system.
- What privacy expectations should children have, and how should a parent's ability to access files or join a desktop be explained?
- Which mistakes must be recoverable, and how much lost work or downtime is acceptable?
- Should a filtering outage block internet access? Which explicit local-network exceptions are needed, and how strict must bypass prevention be for applications and encrypted tunnels? VPN outages already have the agreed blocking behavior above.

# Small companies

The administrator is an owner, a technically capable employee or an IT provider. Everyday users are employees and contractors. The company wants consistent environments and continuity of work without maintaining each person's software separately.

## C1: Give a new team member a ready workspace

An administrator prepares a role-appropriate environment, creates a desktop for a new team member and grants access. The person opens it from their device and begins working with the expected applications and initial configuration.

Success means repeatable setup while keeping individual files and credentials separate. A reusable environment must not accidentally distribute another person's browser session or secrets. Required identity integration and sign-in simplicity remain questions rather than assumed requirements.

## C2: Maintain personal desktops centrally

The administrator updates common software and allocates server resources across the team's desktops. Employees keep their files and personal setup and can reconnect from another supported device.

Success means predictable maintenance, understandable downtime and continuity of work. The team must establish application compatibility, capacity and acceptable performance for its real workloads before treating a browser desktop as a replacement for its existing computers.

## C3: Share a desktop for a task

A small team shares an environment for a project, training session or operational task. A colleague joins the same desktop to demonstrate a workflow or help resolve a problem.

Success means everyone understands that they are using the same session, including its application logins. Granting access to a desktop does not create separate identities inside its applications. Whether a particular task is suitable for shared credentials and simultaneous control must be assessed for that task.

## C4: Handle absence, departure and handover

An employee changes roles or leaves. The administrator removes access, retains work according to the company's needs and makes relevant material available to a successor.

Success means treating access removal, connection termination, retention, application credential revocation and handover as distinct responsibilities. Removing Deskyard access alone should not be described as revoking credentials in external services. Shared project work may need a different lifecycle from an individual's desktop.

## Company questions

- Which roles and applications fit this environment, and which require local hardware, peripherals or another operating system?
- Are personal desktops, shared task desktops or both the primary pattern?
- Who owns the files, who may access an employee's desktop and what should users be told about administrator access?
- What backup, retention, availability and maintenance expectations must the deployment meet?
- Are the current account and access model sufficient, or are groups, delegated administration, identity integration or audit records needed?

# Shared needs and differences

| Need | Family example | Small-company example |
| --- | --- | --- |
| Repeatable setup | Prepare learning apps for siblings | Prepare tools for a role |
| Personal continuity | Return to homework from another device | Continue work after changing devices |
| Central maintenance | Parent maintains the common environment | Administrator updates team environments |
| Help in context | Parent joins a child's desktop | Colleague joins to help or train |
| Recovery | Restore an accidentally deleted project | Restore work after a mistake or failure |
| Access lifecycle | Add a family member or change permissions | Onboard, change roles and offboard |
| Policy expectations | Discuss autonomy and any parental limits | Establish ownership, access and retention rules |

Explore these together, but do not assume that a parental role and a company administrator need identical authority or controls. A family-first implementation can reveal useful shared capabilities without committing the project to every business administration feature.

# Existing foundation

The current implementation provides building blocks for these scenarios:

- [[Architecture/Accounts]] provides administrator and regular accounts with per-desktop access. Administrators can access all desktops; two regular users sharing a desktop share its environment rather than receiving separate operating-system users (`supervisor/internal/accounts/accounts.go`).
- [[Architecture/Desktop]] and [[Architecture/Workspace folder]] provide a container desktop and a persistent home folder. Persistence is limited to `/workspace`; backups require a separate procedure (`supervisor/internal/desktops/service.go`, `images/debian-xfce/`).
- [[Architecture/Image catalog]] and [[Architecture/Recipes]] provide reusable images and custom environments. Home templates initialize files without overwriting existing files; they do not continuously enforce every user's settings (`supervisor/internal/recipes/`, `images/debian-xfce/rootfs/`).
- [[Architecture/Lifecycle]] provides central creation, start, stop, upgrade and removal, with CPU and memory settings (`supervisor/internal/desktops/service.go`).
- [[Architecture/Web UI]] and [[Architecture/Desktop proxy]] provide browser access to the shared desktop. People with access open the same session; there is no independent desktop session for each viewer.

These capabilities do not establish that backups, parental policies, business governance or all desired client devices are supported. Define and verify those needs before describing an end-to-end use case as ready.

# Next exploration

Start with the actual household workflow: who needs a desktop, what they will do in it and which devices they will connect from. Use that example and a comparable small-team workflow to evaluate each shared need. Record confirmed requirements and priorities here, then link approved feature specs from this page as concrete designs emerge.
