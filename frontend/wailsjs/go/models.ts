export namespace main {
	
	export class AsarInfo {
	    path: string;
	    version: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new AsarInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.version = source["version"];
	        this.size = source["size"];
	    }
	}
	export class Patch {
	    file: string;
	    find: string;
	    replace: string;
	    type?: string;
	
	    static createFrom(source: any = {}) {
	        return new Patch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.find = source["find"];
	        this.replace = source["replace"];
	        this.type = source["type"];
	    }
	}
	export class PatchFile {
	    version: string;
	    patches: Patch[];
	
	    static createFrom(source: any = {}) {
	        return new PatchFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.patches = this.convertValues(source["patches"], Patch);
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
	export class PatchResult {
	    success: boolean;
	    message: string;
	    newSize: number;
	    path: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new PatchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.newSize = source["newSize"];
	        this.path = source["path"];
	        this.version = source["version"];
	    }
	}
	export class UpdateBlockResult {
	    success: boolean;
	    blocked: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateBlockResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.blocked = source["blocked"];
	        this.message = source["message"];
	    }
	}
	export class UpdateBlockStatus {
	    supported: boolean;
	    blocked: boolean;
	    installDir?: string;
	    updateExe?: string;
	    backupPath?: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateBlockStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.blocked = source["blocked"];
	        this.installDir = source["installDir"];
	        this.updateExe = source["updateExe"];
	        this.backupPath = source["backupPath"];
	        this.message = source["message"];
	    }
	}

}

