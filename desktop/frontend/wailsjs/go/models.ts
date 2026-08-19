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
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.language = source["language"];
	        this.last_database_path = source["last_database_path"];
	        this.auto_lock_minutes = source["auto_lock_minutes"];
	    }
	}
	export class AttachmentDTO {
	    blob_id: string;
	    workspace_id: string;
	    size_bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.blob_id = source["blob_id"];
	        this.workspace_id = source["workspace_id"];
	        this.size_bytes = source["size_bytes"];
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

}

