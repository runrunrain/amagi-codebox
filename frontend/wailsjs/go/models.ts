export namespace amagi {
	
	export class AmagiCapabilityOverride {
	    vision?: boolean;
	    tool_use?: boolean;
	    tool_use_3way?: boolean;
	    max_output_tokens?: number;
	    thinking_budget_tokens?: number;
	    computer_use?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AmagiCapabilityOverride(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vision = source["vision"];
	        this.tool_use = source["tool_use"];
	        this.tool_use_3way = source["tool_use_3way"];
	        this.max_output_tokens = source["max_output_tokens"];
	        this.thinking_budget_tokens = source["thinking_budget_tokens"];
	        this.computer_use = source["computer_use"];
	    }
	}
	export class AmagiThinking {
	    type: string;
	    budget_tokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new AmagiThinking(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.budget_tokens = source["budget_tokens"];
	    }
	}
	export class AmagiModelPreset {
	    provider: string;
	    model: string;
	    temperature?: number;
	    max_tokens?: number;
	    effort_level?: string;
	    thinking?: AmagiThinking;
	    protocol_options?: Record<string, any>;
	    provider_options?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new AmagiModelPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.temperature = source["temperature"];
	        this.max_tokens = source["max_tokens"];
	        this.effort_level = source["effort_level"];
	        this.thinking = this.convertValues(source["thinking"], AmagiThinking);
	        this.protocol_options = source["protocol_options"];
	        this.provider_options = source["provider_options"];
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
	export class AmagiProvider {
	    protocol: string;
	    base_url?: string;
	
	    static createFrom(source: any = {}) {
	        return new AmagiProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.protocol = source["protocol"];
	        this.base_url = source["base_url"];
	    }
	}
	export class ModelPresetGroup {
	    description?: string;
	    default_preset?: string;
	    presets: Record<string, AmagiModelPreset>;
	
	    static createFrom(source: any = {}) {
	        return new ModelPresetGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.description = source["description"];
	        this.default_preset = source["default_preset"];
	        this.presets = this.convertValues(source["presets"], AmagiModelPreset, true);
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
	export class AmagiSettings {
	    model: string;
	    providers: Record<string, AmagiProvider>;
	    available_models?: string[];
	    model_overrides?: Record<string, string>;
	    model_capability_overrides?: Record<string, AmagiCapabilityOverride>;
	    model_presets: Record<string, ModelPresetGroup>;
	    always_thinking_enabled?: boolean;
	    effort_level?: string;
	    advisor_model?: string;
	
	    static createFrom(source: any = {}) {
	        return new AmagiSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.providers = this.convertValues(source["providers"], AmagiProvider, true);
	        this.available_models = source["available_models"];
	        this.model_overrides = source["model_overrides"];
	        this.model_capability_overrides = this.convertValues(source["model_capability_overrides"], AmagiCapabilityOverride, true);
	        this.model_presets = this.convertValues(source["model_presets"], ModelPresetGroup, true);
	        this.always_thinking_enabled = source["always_thinking_enabled"];
	        this.effort_level = source["effort_level"];
	        this.advisor_model = source["advisor_model"];
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

export namespace config {
	
	export class AgentTeamsConfig {
	    enabled: boolean;
	    teammate_mode: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentTeamsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.teammate_mode = source["teammate_mode"];
	    }
	}
	export class ContextWindowConfig {
	    model_context_window?: number;
	    model_auto_compact_token_limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new ContextWindowConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model_context_window = source["model_context_window"];
	        this.model_auto_compact_token_limit = source["model_auto_compact_token_limit"];
	    }
	}
	export class ThinkingConfig {
	    type: string;
	    budgetTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new ThinkingConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.budgetTokens = source["budgetTokens"];
	    }
	}
	export class Parameters {
	    temperature?: number;
	    top_p?: number;
	    max_tokens?: number;
	    max_context_length?: number;
	    do_sample?: boolean;
	    thinking?: ThinkingConfig;
	    stream?: boolean;
	    context_window?: ContextWindowConfig;
	
	    static createFrom(source: any = {}) {
	        return new Parameters(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.temperature = source["temperature"];
	        this.top_p = source["top_p"];
	        this.max_tokens = source["max_tokens"];
	        this.max_context_length = source["max_context_length"];
	        this.do_sample = source["do_sample"];
	        this.thinking = this.convertValues(source["thinking"], ThinkingConfig);
	        this.stream = source["stream"];
	        this.context_window = this.convertValues(source["context_window"], ContextWindowConfig);
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
	export class Preset {
	    name: string;
	    model: string;
	    parameters: Parameters;
	    target?: string;
	    opencode_config?: number[];
	
	    static createFrom(source: any = {}) {
	        return new Preset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.model = source["model"];
	        this.parameters = this.convertValues(source["parameters"], Parameters);
	        this.target = source["target"];
	        this.opencode_config = source["opencode_config"];
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
	export class Provider {
	    type?: string;
	    base_url: string;
	    default_model: string;
	    auth_key: string;
	    presets: Record<string, Preset>;
	    url_history?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.base_url = source["base_url"];
	        this.default_model = source["default_model"];
	        this.auth_key = source["auth_key"];
	        this.presets = this.convertValues(source["presets"], Preset, true);
	        this.url_history = source["url_history"];
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
	export class AppConfig {
	    models: Record<string, Provider>;
	    agent_teams: AgentTeamsConfig;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new AppConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = this.convertValues(source["models"], Provider, true);
	        this.agent_teams = this.convertValues(source["agent_teams"], AgentTeamsConfig);
	        this.version = source["version"];
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
	export class ConfigService {
	
	
	    static createFrom(source: any = {}) {
	        return new ConfigService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}
	
	
	
	

}

export namespace envvars {
	
	export class EnvVar {
	    key: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new EnvVar(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	    }
	}

}

export namespace logging {
	
	export class Entry {
	    time: string;
	    level: string;
	    source: string;
	    message: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.source = source["source"];
	        this.message = source["message"];
	        this.detail = source["detail"];
	    }
	}

}

export namespace paths {
	
	export class PathEntry {
	    path: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new PathEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.label = source["label"];
	    }
	}
	export class PathsService {
	
	
	    static createFrom(source: any = {}) {
	        return new PathsService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace plugin {
	
	export class AgentInfo {
	    name: string;
	    description: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.filePath = source["filePath"];
	    }
	}
	export class CommandInfo {
	    name: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.filePath = source["filePath"];
	    }
	}
	export class CommandResult {
	    success: boolean;
	    output: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.output = source["output"];
	        this.error = source["error"];
	    }
	}
	export class HookInfo {
	    name: string;
	    event: string;
	    type: string;
	    command?: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new HookInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.event = source["event"];
	        this.type = source["type"];
	        this.command = source["command"];
	        this.filePath = source["filePath"];
	    }
	}
	export class InstalledPlugin {
	    id: string;
	    name: string;
	    marketplace: string;
	    version: string;
	    scope: string;
	    enabled: boolean;
	    installPath: string;
	    installedAt: string;
	    lastUpdated: string;
	    gitCommitSha?: string;
	
	    static createFrom(source: any = {}) {
	        return new InstalledPlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.marketplace = source["marketplace"];
	        this.version = source["version"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.installedAt = source["installedAt"];
	        this.lastUpdated = source["lastUpdated"];
	        this.gitCommitSha = source["gitCommitSha"];
	    }
	}
	export class Marketplace {
	    name: string;
	    source: string;
	    repo?: string;
	    url?: string;
	    installLocation: string;
	    lastUpdated?: string;
	    autoUpdate?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Marketplace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.repo = source["repo"];
	        this.url = source["url"];
	        this.installLocation = source["installLocation"];
	        this.lastUpdated = source["lastUpdated"];
	        this.autoUpdate = source["autoUpdate"];
	    }
	}
	export class SubItem {
	    type: string;
	    name: string;
	    path: string;
	    enabled: boolean;
	    globallyEnabled?: boolean;
	    selectable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SubItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.enabled = source["enabled"];
	        this.globallyEnabled = source["globallyEnabled"];
	        this.selectable = source["selectable"];
	    }
	}
	export class SkillInfo {
	    name: string;
	    description: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.filePath = source["filePath"];
	    }
	}
	export class PluginManifest {
	    name: string;
	    version: string;
	    description: string;
	    author?: Record<string, string>;
	    license?: string;
	    keywords?: string[];
	    homepage?: string;
	    repository?: string;
	
	    static createFrom(source: any = {}) {
	        return new PluginManifest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.author = source["author"];
	        this.license = source["license"];
	        this.keywords = source["keywords"];
	        this.homepage = source["homepage"];
	        this.repository = source["repository"];
	    }
	}
	export class PluginDetail {
	    id: string;
	    name: string;
	    marketplace: string;
	    version: string;
	    scope: string;
	    enabled: boolean;
	    installPath: string;
	    installedAt: string;
	    lastUpdated: string;
	    gitCommitSha?: string;
	    manifest: PluginManifest;
	    skills: SkillInfo[];
	    agents: AgentInfo[];
	    commands: CommandInfo[];
	    hooks: HookInfo[];
	    hasMcp: boolean;
	    mcpServers?: Record<string, any>;
	    pluginType: string;
	    hasClaudeMd: boolean;
	    claudeMdPath?: string;
	    subItems: SubItem[];
	
	    static createFrom(source: any = {}) {
	        return new PluginDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.marketplace = source["marketplace"];
	        this.version = source["version"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.installedAt = source["installedAt"];
	        this.lastUpdated = source["lastUpdated"];
	        this.gitCommitSha = source["gitCommitSha"];
	        this.manifest = this.convertValues(source["manifest"], PluginManifest);
	        this.skills = this.convertValues(source["skills"], SkillInfo);
	        this.agents = this.convertValues(source["agents"], AgentInfo);
	        this.commands = this.convertValues(source["commands"], CommandInfo);
	        this.hooks = this.convertValues(source["hooks"], HookInfo);
	        this.hasMcp = source["hasMcp"];
	        this.mcpServers = source["mcpServers"];
	        this.pluginType = source["pluginType"];
	        this.hasClaudeMd = source["hasClaudeMd"];
	        this.claudeMdPath = source["claudeMdPath"];
	        this.subItems = this.convertValues(source["subItems"], SubItem);
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
	
	export class SubItemRef {
	    type: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new SubItemRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.name = source["name"];
	    }
	}
	export class PluginSubItemState {
	    pluginId: string;
	    disabledSubItems: SubItemRef[];
	
	    static createFrom(source: any = {}) {
	        return new PluginSubItemState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.disabledSubItems = this.convertValues(source["disabledSubItems"], SubItemRef);
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

export namespace proxy {
	
	export class InjectionLog {
	    time: string;
	    ruleNames: string[];
	    preview: string;
	    status: number;
	
	    static createFrom(source: any = {}) {
	        return new InjectionLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.ruleNames = source["ruleNames"];
	        this.preview = source["preview"];
	        this.status = source["status"];
	    }
	}
	export class InjectionRule {
	    id: string;
	    name: string;
	    keywords: string[];
	    prompt: string;
	    enabled: boolean;
	    priority: number;
	    enableCache: boolean;
	    cacheTtl: string;
	
	    static createFrom(source: any = {}) {
	        return new InjectionRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.keywords = source["keywords"];
	        this.prompt = source["prompt"];
	        this.enabled = source["enabled"];
	        this.priority = source["priority"];
	        this.enableCache = source["enableCache"];
	        this.cacheTtl = source["cacheTtl"];
	    }
	}
	export class ProxyStatus {
	    running: boolean;
	    port: number;
	    backendURL: string;
	    ruleCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ProxyStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.port = source["port"];
	        this.backendURL = source["backendURL"];
	        this.ruleCount = source["ruleCount"];
	    }
	}

}

export namespace session {
	
	export class SessionInfo {
	    id: string;
	    appType: string;
	    provider: string;
	    preset: string;
	    model: string;
	    mode: string;
	    workDir: string;
	    status: string;
	    pid: number;
	    startedAt: string;
	    duration: string;
	    useProxy: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.appType = source["appType"];
	        this.provider = source["provider"];
	        this.preset = source["preset"];
	        this.model = source["model"];
	        this.mode = source["mode"];
	        this.workDir = source["workDir"];
	        this.status = source["status"];
	        this.pid = source["pid"];
	        this.startedAt = source["startedAt"];
	        this.duration = source["duration"];
	        this.useProxy = source["useProxy"];
	    }
	}

}

export namespace settings {
	
	export class TerminalSettings {
	    scrollback: number;
	
	    static createFrom(source: any = {}) {
	        return new TerminalSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scrollback = source["scrollback"];
	    }
	}
	export class ShellEntry {
	    path: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new ShellEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.label = source["label"];
	    }
	}
	export class DashboardDefaults {
	    provider: string;
	    preset: string;
	    openCodeProvider: string;
	    openCodePreset: string;
	    mode: string;
	    shell: string;
	    claudeMode: string;
	    claudeShell: string;
	    openCodeMode: string;
	    openCodeShell: string;
	    codexMode: string;
	    codexShell: string;
	    amagiCodePreset: string;
	    amagiCodeMode: string;
	    amagiCodeShell: string;
	    useProxy: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DashboardDefaults(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.preset = source["preset"];
	        this.openCodeProvider = source["openCodeProvider"];
	        this.openCodePreset = source["openCodePreset"];
	        this.mode = source["mode"];
	        this.shell = source["shell"];
	        this.claudeMode = source["claudeMode"];
	        this.claudeShell = source["claudeShell"];
	        this.openCodeMode = source["openCodeMode"];
	        this.openCodeShell = source["openCodeShell"];
	        this.codexMode = source["codexMode"];
	        this.codexShell = source["codexShell"];
	        this.amagiCodePreset = source["amagiCodePreset"];
	        this.amagiCodeMode = source["amagiCodeMode"];
	        this.amagiCodeShell = source["amagiCodeShell"];
	        this.useProxy = source["useProxy"];
	    }
	}
	export class AppSettings {
	    dashboard: DashboardDefaults;
	    shellPaths: ShellEntry[];
	    terminal: TerminalSettings;
	    remoteHost: string;
	    remotePort: number;
	    mobileWebRoot: string;
	    githubToken: string;
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dashboard = this.convertValues(source["dashboard"], DashboardDefaults);
	        this.shellPaths = this.convertValues(source["shellPaths"], ShellEntry);
	        this.terminal = this.convertValues(source["terminal"], TerminalSettings);
	        this.remoteHost = source["remoteHost"];
	        this.remotePort = source["remotePort"];
	        this.mobileWebRoot = source["mobileWebRoot"];
	        this.githubToken = source["githubToken"];
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
	
	export class Service {
	
	
	    static createFrom(source: any = {}) {
	        return new Service(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}
	

}

export namespace updater {
	
	export class UpdateInfo {
	    hasUpdate: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    releaseNotes: string;
	    publishedAt: string;
	    downloadURL: string;
	    assetSize: number;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasUpdate = source["hasUpdate"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.releaseNotes = source["releaseNotes"];
	        this.publishedAt = source["publishedAt"];
	        this.downloadURL = source["downloadURL"];
	        this.assetSize = source["assetSize"];
	    }
	}

}

export namespace workspace {
	
	export class AvailablePlugin {
	    id: string;
	    name: string;
	    marketplace: string;
	    version: string;
	    scope: string;
	    enabled: boolean;
	    installPath: string;
	    installedAt: string;
	    lastUpdated: string;
	    gitCommitSha?: string;
	    manifest: plugin.PluginManifest;
	    skills: plugin.SkillInfo[];
	    agents: plugin.AgentInfo[];
	    commands: plugin.CommandInfo[];
	    hooks: plugin.HookInfo[];
	    hasMcp: boolean;
	    mcpServers?: Record<string, any>;
	    pluginType: string;
	    hasClaudeMd: boolean;
	    claudeMdPath?: string;
	    subItems: plugin.SubItem[];
	    globallyEnabledAll: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AvailablePlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.marketplace = source["marketplace"];
	        this.version = source["version"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.installedAt = source["installedAt"];
	        this.lastUpdated = source["lastUpdated"];
	        this.gitCommitSha = source["gitCommitSha"];
	        this.manifest = this.convertValues(source["manifest"], plugin.PluginManifest);
	        this.skills = this.convertValues(source["skills"], plugin.SkillInfo);
	        this.agents = this.convertValues(source["agents"], plugin.AgentInfo);
	        this.commands = this.convertValues(source["commands"], plugin.CommandInfo);
	        this.hooks = this.convertValues(source["hooks"], plugin.HookInfo);
	        this.hasMcp = source["hasMcp"];
	        this.mcpServers = source["mcpServers"];
	        this.pluginType = source["pluginType"];
	        this.hasClaudeMd = source["hasClaudeMd"];
	        this.claudeMdPath = source["claudeMdPath"];
	        this.subItems = this.convertValues(source["subItems"], plugin.SubItem);
	        this.globallyEnabledAll = source["globallyEnabledAll"];
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
	export class DeploymentEntry {
	    pluginId: string;
	    pluginVersion: string;
	    subItemRef: plugin.SubItemRef;
	    targetPath: string;
	    mergeType: string;
	    status: string;
	    checksum: string;
	    deployedAt: string;
	    contentMarker?: string;
	    mergeOrder?: number;
	    sourceScope: string;
	
	    static createFrom(source: any = {}) {
	        return new DeploymentEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.pluginVersion = source["pluginVersion"];
	        this.subItemRef = this.convertValues(source["subItemRef"], plugin.SubItemRef);
	        this.targetPath = source["targetPath"];
	        this.mergeType = source["mergeType"];
	        this.status = source["status"];
	        this.checksum = source["checksum"];
	        this.deployedAt = source["deployedAt"];
	        this.contentMarker = source["contentMarker"];
	        this.mergeOrder = source["mergeOrder"];
	        this.sourceScope = source["sourceScope"];
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
	export class DeploymentManifest {
	    version: string;
	    generatedAt: string;
	    entries: DeploymentEntry[];
	
	    static createFrom(source: any = {}) {
	        return new DeploymentManifest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.generatedAt = source["generatedAt"];
	        this.entries = this.convertValues(source["entries"], DeploymentEntry);
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
	export class CleanResult {
	    targetId: string;
	    warnings: string[];
	    manifest: DeploymentManifest;
	    removed: string[];
	
	    static createFrom(source: any = {}) {
	        return new CleanResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetId = source["targetId"];
	        this.warnings = source["warnings"];
	        this.manifest = this.convertValues(source["manifest"], DeploymentManifest);
	        this.removed = source["removed"];
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
	export class Conflict {
	    type: string;
	    pluginId?: string;
	    targetPath?: string;
	    message: string;
	    blocking: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Conflict(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.pluginId = source["pluginId"];
	        this.targetPath = source["targetPath"];
	        this.message = source["message"];
	        this.blocking = source["blocking"];
	    }
	}
	export class DeployResult {
	    targetId: string;
	    warnings: string[];
	    conflicts: Conflict[];
	    manifest: DeploymentManifest;
	    deployed: DeploymentEntry[];
	    removed: string[];
	
	    static createFrom(source: any = {}) {
	        return new DeployResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetId = source["targetId"];
	        this.warnings = source["warnings"];
	        this.conflicts = this.convertValues(source["conflicts"], Conflict);
	        this.manifest = this.convertValues(source["manifest"], DeploymentManifest);
	        this.deployed = this.convertValues(source["deployed"], DeploymentEntry);
	        this.removed = source["removed"];
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
	
	
	export class GlobalEnabled {
	    pluginId: string;
	    enabledAll: boolean;
	    enabledSubItems: plugin.SubItemRef[];
	    tools: string[];
	    deployedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new GlobalEnabled(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.enabledAll = source["enabledAll"];
	        this.enabledSubItems = this.convertValues(source["enabledSubItems"], plugin.SubItemRef);
	        this.tools = source["tools"];
	        this.deployedAt = source["deployedAt"];
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
	export class WorkspacePlugin {
	    pluginId: string;
	    enabledSubItems: plugin.SubItemRef[];
	    deployScope: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspacePlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.enabledSubItems = this.convertValues(source["enabledSubItems"], plugin.SubItemRef);
	        this.deployScope = source["deployScope"];
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
	export class Workspace {
	    id: string;
	    name: string;
	    path: string;
	    tools: string[];
	    plugins: WorkspacePlugin[];
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.tools = source["tools"];
	        this.plugins = this.convertValues(source["plugins"], WorkspacePlugin);
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
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

