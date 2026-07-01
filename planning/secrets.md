### (1) Project Scope and Context

The objective is to replace the insecure practice of hardcoding plain-text usernames and passwords for `curl` executions with a secure, encrypted storage mechanism. This will be achieved by integrating the `github.com/OpenCHAMI/magellan/pkg/secrets` library, specifically utilizing its `LocalSecretStore` implementation.

To prevent plaintext credentials from being captured by middleware, reverse proxies, or system audit trails, the running service will operate strictly as a read-only consumer of the secret store. The secrets will be populated out-of-band using a dedicated administrative CLI tool or deployment pipeline. This tool will use a 64-character hex-encoded AES-256 GCM master key (`MASTER_KEY`) to encrypt the required credentials and write the resulting encrypted `secrets.json` file directly to a storage volume.

The encrypted file will then be mounted or provisioned to the runtime environment of the Fabrica service. The application will instantiate the secret store once, maintaining it in the global state. At execution time, the application will retrieve and decrypt the credentials, deserialize them from JSON format, and inject them dynamically into the `curl` command.

Because this project is a Fabrica-based service, modifying the data model to remove plain-text credential fields requires updating the resource specifications and subsequently running `fabrica generate` to ensure the generated boilerplate remains functional and synchronized with the updated schema.

### (2) Implementation Code Changes

To implement this specification, the following discrete changes must be made to the codebase:

* **Resource Model Updates:**
* Locate the Fabrica resource definition files (e.g., `apis.yaml` or relevant `.yaml` files in `apis/`).
* Remove the `username` and `password` properties from the definitions.
* Run `fabrica generate` at the project root to update `cmd/server/models_generated.go`, `cmd/server/routes_generated.go`, and any dependent internal storage packages.


* **Out-of-Band Credential Ingestion (New CLI Tool):**
* Create a standalone administrative utility (e.g., `cmd/secret-cli/main.go`).
* This utility must import `github.com/OpenCHAMI/magellan/pkg/secrets` and read the `MASTER_KEY` environment variable.
* The utility will accept plaintext credentials via secure local input (e.g., CLI arguments or standard input), serialize them into a JSON string (`{"username":"<user>","password":"<pass>"}`), and use `StoreSecretByID(secretID, credentials)` to generate or update the encrypted `secrets.json` file on disk.


* **Service Environment Configuration & State Initialization:**
* In the server initialization block (e.g., `cmd/server/main.go`), import `github.com/OpenCHAMI/magellan/pkg/secrets`.
* Read the `MASTER_KEY` environment variable. If it is empty or invalid, the application must terminate with a fatal error.
* Verify the existence of the provisioned `secrets.json` file on the filesystem.
* Call `secrets.OpenStore("secrets.json")` and attach the returned `SecretStore` interface to the central application state/context. Ensure this is instantiated exactly once to avoid I/O conflicts.


* **Credential Retrieval and Execution Logic:**
* Ensure the service exposes zero HTTP endpoints capable of accepting or modifying plaintext credentials.
* Locate the function responsible for building and executing the `curl` command.
* Call `GetSecretByID(secretID)` using the application state's `SecretStore` instance to retrieve the decrypted JSON string.
* Parse the JSON string back into discrete `username` and `password` variables.
* Inject these variables into the `curl` subprocess arguments (e.g., `-u`, `username:password`).



### (3) Acceptance Criteria

* **AC1:** The application fails to start if the `MASTER_KEY` environment variable is not set to a valid 64-character hex-encoded string.
* **AC2:** Plain-text fields for `username` and `password` are removed from the Fabrica resource models, and `fabrica generate` completes without syntax or generation errors.
* **AC3:** The `SecretStore` interface is instantiated only once by the service, acting strictly in a read-only capacity.
* **AC4:** The service exposes no HTTP endpoints or network interfaces that accept plaintext credentials for storage.
* **AC5:** An out-of-band CLI tool or script is implemented that successfully takes a plaintext username and password, encrypts the serialized JSON payload via `StoreSecretByID`, and writes it to the target `secrets.json` file.
* **AC6:** Execution of the `curl` workflow successfully retrieves and decrypts the credentials via `GetSecretByID` without reading plain-text from the file system.
* **AC7:** The application parses the decrypted JSON payload and dynamically populates the `curl` command.
* **AC8:** The implemented `curl` execution is manually tested and verified to succeed against the target endpoint using the dynamically retrieved credentials.

### (4) Handoff Document Generation

```markdown
## Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-secrets.md` file in the planning directory containing:

1. A brief summary of the implemented logic, detailing the separation of concerns between the out-of-band credential generation tool and the read-only Fabrica service consumption.
2. The file path of the chosen encrypted JSON store and how the application state manages the `SecretStore` interface.
3. The exact, verified `curl` command that successfully tested the code.
4. Detailed notes on important details for using the code that was implemented. This must include:
    * Instructions on generating a valid 64-character hex-encoded `MASTER_KEY` and setting it in the environment.
    * Documentation on how to execute the out-of-band CLI tool to generate the `secrets.json` file.
    * Instructions on how to properly mount or provision the resulting `secrets.json` file into the service's runtime environment.

```