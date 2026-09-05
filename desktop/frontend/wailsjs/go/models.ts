export namespace main {
	
	export class AccountInfo {
	    account_id: string;
	    device_id: string;
	    workspace_id: string;
	    key_protection: string;
	
	    static createFrom(source: any = {}) {
	        return new AccountInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.account_id = source["account_id"];
	        this.device_id = source["device_id"];
	        this.workspace_id = source["workspace_id"];
	        this.key_protection = source["key_protection"];
	    }
	}
	export class AccountStatus {
	    unlocked: boolean;
	    account?: AccountInfo;
	
	    static createFrom(source: any = {}) {
	        return new AccountStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.unlocked = source["unlocked"];
	        this.account = this.convertValues(source["account"], AccountInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AppSettings {
	    language: string;
	    last_database_path: string;
	    auto_lock_minutes: number;
	    backup_directory: string;
	    quick_note_hotkey: string;
	    autostart_enabled: boolean;
	    sync_enabled: boolean;
	    sync_server_url: string;
	    sync_security_mode: string;
	    sync_fingerprint: string;
	    active_workspace_id: string;
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.language = source["language"];
	        this.last_database_path = source["last_database_path"];
	        this.auto_lock_minutes = source["auto_lock_minutes"];
	        this.backup_directory = source["backup_directory"];
	        this.quick_note_hotkey = source["quick_note_hotkey"];
	        this.autostart_enabled = source["autostart_enabled"];
	        this.sync_enabled = source["sync_enabled"];
	        this.sync_server_url = source["sync_server_url"];
	        this.sync_security_mode = source["sync_security_mode"];
	        this.sync_fingerprint = source["sync_fingerprint"];
	        this.active_workspace_id = source["active_workspace_id"];
	    }
	}
	export class AttachmentDTO {
	    blob_id: string;
	    workspace_id: string;
	    display_name: string;
	    media_type: string;
	    size_bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.blob_id = source["blob_id"];
	        this.workspace_id = source["workspace_id"];
	        this.display_name = source["display_name"];
	        this.media_type = source["media_type"];
	        this.size_bytes = source["size_bytes"];
	    }
	}
	export class AttachmentPreviewDTO {
	    display_name: string;
	    media_type: string;
	    data_base64: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentPreviewDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display_name = source["display_name"];
	        this.media_type = source["media_type"];
	        this.data_base64 = source["data_base64"];
	    }
	}
	export class AttachmentSaveResult {
	    display_name: string;
	    media_type: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentSaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display_name = source["display_name"];
	        this.media_type = source["media_type"];
	    }
	}
	export class AutostartStatusDTO {
	    enabled: boolean;
	    conflict_path: string;
	
	    static createFrom(source: any = {}) {
	        return new AutostartStatusDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.conflict_path = source["conflict_path"];
	    }
	}
	export class BackupDTO {
	    id: string;
	    kind: string;
	    location: string;
	    verified_unix_ms?: number;
	    note_count?: number;
	    size_bytes?: number;
	    created_unix_ms: number;
	    corrupt: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BackupDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.location = source["location"];
	        this.verified_unix_ms = source["verified_unix_ms"];
	        this.note_count = source["note_count"];
	        this.size_bytes = source["size_bytes"];
	        this.created_unix_ms = source["created_unix_ms"];
	        this.corrupt = source["corrupt"];
	    }
	}
	export class BackupPreviewDTO {
	    backup: BackupDTO;
	    note_titles: string[];
	
	    static createFrom(source: any = {}) {
	        return new BackupPreviewDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.backup = this.convertValues(source["backup"], BackupDTO);
	        this.note_titles = source["note_titles"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CommitNoteBodyRequest {
	    note_id: string;
	    update_base64: string;
	    update_format: string;
	    title?: string;
	
	    static createFrom(source: any = {}) {
	        return new CommitNoteBodyRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.note_id = source["note_id"];
	        this.update_base64 = source["update_base64"];
	        this.update_format = source["update_format"];
	        this.title = source["title"];
	    }
	}
	export class ConnectServerRequest {
	    url: string;
	    invite_code: string;
	    fingerprint: string;
	    security_mode: string;
	    qr_code: string;
	    device_name: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectServerRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.invite_code = source["invite_code"];
	        this.fingerprint = source["fingerprint"];
	        this.security_mode = source["security_mode"];
	        this.qr_code = source["qr_code"];
	        this.device_name = source["device_name"];
	    }
	}
	export class CreateAccountRequest {
	    database_path: string;
	    passphrase: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateAccountRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.database_path = source["database_path"];
	        this.passphrase = source["passphrase"];
	    }
	}
	export class DiffLineDTO {
	    op: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffLineDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.op = source["op"];
	        this.text = source["text"];
	    }
	}
	export class ExportManifestDTO {
	    version: number;
	    exported_unix_ms: number;
	    note_count: number;
	
	    static createFrom(source: any = {}) {
	        return new ExportManifestDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.exported_unix_ms = source["exported_unix_ms"];
	        this.note_count = source["note_count"];
	    }
	}
	export class GCBlobCandidateDTO {
	    blob_id: string;
	    size_bytes: number;
	    orphaned_unix_ms: number;
	    in_any_backup: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GCBlobCandidateDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.blob_id = source["blob_id"];
	        this.size_bytes = source["size_bytes"];
	        this.orphaned_unix_ms = source["orphaned_unix_ms"];
	        this.in_any_backup = source["in_any_backup"];
	    }
	}
	export class GCNoteCandidateDTO {
	    note_id: string;
	    title: string;
	    deleted_unix_ms: number;
	
	    static createFrom(source: any = {}) {
	        return new GCNoteCandidateDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.note_id = source["note_id"];
	        this.title = source["title"];
	        this.deleted_unix_ms = source["deleted_unix_ms"];
	    }
	}
	export class GCReportDTO {
	    blobs: GCBlobCandidateDTO[];
	    notes: GCNoteCandidateDTO[];
	    blob_bytes_reclaimed: number;
	    dry_run: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GCReportDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.blobs = this.convertValues(source["blobs"], GCBlobCandidateDTO);
	        this.notes = this.convertValues(source["notes"], GCNoteCandidateDTO);
	        this.blob_bytes_reclaimed = source["blob_bytes_reclaimed"];
	        this.dry_run = source["dry_run"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportWarningDTO {
	    note_title: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportWarningDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.note_title = source["note_title"];
	        this.message = source["message"];
	    }
	}
	export class ImportResultDTO {
	    new_note_ids: string[];
	    warnings: ImportWarningDTO[];
	
	    static createFrom(source: any = {}) {
	        return new ImportResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.new_note_ids = source["new_note_ids"];
	        this.warnings = this.convertValues(source["warnings"], ImportWarningDTO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class LocaleCatalog {
	    locale: string;
	    strings: Record<string, string>;
	    supported: string[];
	
	    static createFrom(source: any = {}) {
	        return new LocaleCatalog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.locale = source["locale"];
	        this.strings = source["strings"];
	        this.supported = source["supported"];
	    }
	}
	export class NoteDTO {
	    id: string;
	    workspace_id: string;
	    notebook_id: string;
	    title: string;
	    pinned: boolean;
	    archived: boolean;
	    deleted: boolean;
	    created_unix_ms: number;
	    updated_unix_ms: number;
	    preview: string;
	
	    static createFrom(source: any = {}) {
	        return new NoteDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workspace_id = source["workspace_id"];
	        this.notebook_id = source["notebook_id"];
	        this.title = source["title"];
	        this.pinned = source["pinned"];
	        this.archived = source["archived"];
	        this.deleted = source["deleted"];
	        this.created_unix_ms = source["created_unix_ms"];
	        this.updated_unix_ms = source["updated_unix_ms"];
	        this.preview = source["preview"];
	    }
	}
	export class NoteDocumentDTO {
	    update_base64: string;
	    format: string;
	
	    static createFrom(source: any = {}) {
	        return new NoteDocumentDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.update_base64 = source["update_base64"];
	        this.format = source["format"];
	    }
	}
	export class NotebookDTO {
	    id: string;
	    workspace_id: string;
	    parent_id: string;
	    name: string;
	    deleted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NotebookDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workspace_id = source["workspace_id"];
	        this.parent_id = source["parent_id"];
	        this.name = source["name"];
	        this.deleted = source["deleted"];
	    }
	}
	export class RestorePlanEntryDTO {
	    note_id: string;
	    title: string;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new RestorePlanEntryDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.note_id = source["note_id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	    }
	}
	export class RestorePlanDTO {
	    entries: RestorePlanEntryDTO[];
	    required_storage_bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new RestorePlanDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries = this.convertValues(source["entries"], RestorePlanEntryDTO);
	        this.required_storage_bytes = source["required_storage_bytes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RestoreResultDTO {
	    safety_backup: BackupDTO;
	    new_note_ids: string[];
	
	    static createFrom(source: any = {}) {
	        return new RestoreResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.safety_backup = this.convertValues(source["safety_backup"], BackupDTO);
	        this.new_note_ids = source["new_note_ids"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RevisionDTO {
	    id: string;
	    checkpoint: boolean;
	    created_unix_ms: number;
	
	    static createFrom(source: any = {}) {
	        return new RevisionDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.checkpoint = source["checkpoint"];
	        this.created_unix_ms = source["created_unix_ms"];
	    }
	}
	export class SavedSearchDTO {
	    id: string;
	    workspace_id: string;
	    name: string;
	    query: string;
	    created_unix_ms: number;
	    updated_unix_ms: number;
	
	    static createFrom(source: any = {}) {
	        return new SavedSearchDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workspace_id = source["workspace_id"];
	        this.name = source["name"];
	        this.query = source["query"];
	        this.created_unix_ms = source["created_unix_ms"];
	        this.updated_unix_ms = source["updated_unix_ms"];
	    }
	}
	export class SearchResultDTO {
	    note: NoteDTO;
	    rank: number;
	
	    static createFrom(source: any = {}) {
	        return new SearchResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.note = this.convertValues(source["note"], NoteDTO);
	        this.rank = source["rank"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ServerConnectionInfo {
	    enabled: boolean;
	    url: string;
	    protocol: string;
	    security_mode: string;
	    fingerprint?: string;
	    diagnostics: transport.Diagnostics;
	
	    static createFrom(source: any = {}) {
	        return new ServerConnectionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.url = source["url"];
	        this.protocol = source["protocol"];
	        this.security_mode = source["security_mode"];
	        this.fingerprint = source["fingerprint"];
	        this.diagnostics = this.convertValues(source["diagnostics"], transport.Diagnostics);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncQuarantineDTO {
	    operation_id: string;
	    sequence: number;
	    reason: string;
	    received_unix_ms: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncQuarantineDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation_id = source["operation_id"];
	        this.sequence = source["sequence"];
	        this.reason = source["reason"];
	        this.received_unix_ms = source["received_unix_ms"];
	    }
	}
	export class TagDTO {
	    id: string;
	    workspace_id: string;
	    name: string;
	    deleted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TagDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workspace_id = source["workspace_id"];
	        this.name = source["name"];
	        this.deleted = source["deleted"];
	    }
	}
	export class UnlockAccountRequest {
	    database_path: string;
	    passphrase: string;
	
	    static createFrom(source: any = {}) {
	        return new UnlockAccountRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.database_path = source["database_path"];
	        this.passphrase = source["passphrase"];
	    }
	}
	export class WorkspaceMemberDTO {
	    user_id: string;
	    display_name: string;
	    role: string;
	    // Go type: time
	    revoked_at?: any;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceMemberDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_id = source["user_id"];
	        this.display_name = source["display_name"];
	        this.role = source["role"];
	        this.revoked_at = this.convertValues(source["revoked_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkspaceSummaryDTO {
	    workspace_id: string;
	    role: string;
	    active: boolean;
	    member_count?: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceSummaryDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace_id = source["workspace_id"];
	        this.role = source["role"];
	        this.active = source["active"];
	        this.member_count = source["member_count"];
	    }
	}

}

export namespace transport {
	
	export class Diagnostics {
	    reachable: boolean;
	    tls_1_3: boolean;
	    authenticated: boolean;
	    latency_ms: number;
	    error_class?: string;
	
	    static createFrom(source: any = {}) {
	        return new Diagnostics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reachable = source["reachable"];
	        this.tls_1_3 = source["tls_1_3"];
	        this.authenticated = source["authenticated"];
	        this.latency_ms = source["latency_ms"];
	        this.error_class = source["error_class"];
	    }
	}
	export class RemoteDevice {
	    device_id: string;
	    user_id: string;
	    display_name: string;
	    signing_public: number[];
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    revoked_at?: any;
	
	    static createFrom(source: any = {}) {
	        return new RemoteDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.device_id = source["device_id"];
	        this.user_id = source["user_id"];
	        this.display_name = source["display_name"];
	        this.signing_public = source["signing_public"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.revoked_at = this.convertValues(source["revoked_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

