export namespace codexplugin {
	
	export class AddMarketplaceRequest {
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new AddMarketplaceRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	    }
	}
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
	export class CodexAvailablePlugin {
	    pluginId: string;
	    name: string;
	    marketplaceName: string;
	    version?: string;
	    description?: string;
	    author?: string;
	    repository?: string;
	    snapshotPath?: string;
	    manifestPath?: string;
	
	    static createFrom(source: any = {}) {
	        return new CodexAvailablePlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.name = source["name"];
	        this.marketplaceName = source["marketplaceName"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.author = source["author"];
	        this.repository = source["repository"];
	        this.snapshotPath = source["snapshotPath"];
	        this.manifestPath = source["manifestPath"];
	    }
	}
	export class CodexMarketplace {
	    name: string;
	    source?: string;
	    repo?: string;
	    url?: string;
	    installLocation?: string;
	    snapshotPath?: string;
	    lastUpdated?: string;
	    rawLine?: string;
	
	    static createFrom(source: any = {}) {
	        return new CodexMarketplace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.repo = source["repo"];
	        this.url = source["url"];
	        this.installLocation = source["installLocation"];
	        this.snapshotPath = source["snapshotPath"];
	        this.lastUpdated = source["lastUpdated"];
	        this.rawLine = source["rawLine"];
	    }
	}
	export class CodexPlugin {
	    id: string;
	    name: string;
	    marketplace: string;
	    version?: string;
	    enabled: boolean;
	    installPath?: string;
	    manifestPath?: string;
	    installedAt?: string;
	    lastUpdated?: string;
	    source?: string;
	    warning?: string;
	    duplicateOf?: string;
	
	    static createFrom(source: any = {}) {
	        return new CodexPlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.marketplace = source["marketplace"];
	        this.version = source["version"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.manifestPath = source["manifestPath"];
	        this.installedAt = source["installedAt"];
	        this.lastUpdated = source["lastUpdated"];
	        this.source = source["source"];
	        this.warning = source["warning"];
	        this.duplicateOf = source["duplicateOf"];
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
	export class CommandInfo {
	    name: string;
	    description: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.filePath = source["filePath"];
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
	export class CodexPluginInterface {
	    displayName?: string;
	    shortDescription?: string;
	    longDescription?: string;
	    developerName?: string;
	    category?: string;
	    capabilities?: string[];
	    websiteURL?: string;
	    defaultPrompt?: string[];
	
	    static createFrom(source: any = {}) {
	        return new CodexPluginInterface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.displayName = source["displayName"];
	        this.shortDescription = source["shortDescription"];
	        this.longDescription = source["longDescription"];
	        this.developerName = source["developerName"];
	        this.category = source["category"];
	        this.capabilities = source["capabilities"];
	        this.websiteURL = source["websiteURL"];
	        this.defaultPrompt = source["defaultPrompt"];
	    }
	}
	export class CodexPluginManifest {
	    name: string;
	    version: string;
	    description: string;
	    author?: Record<string, string>;
	    license?: string;
	    keywords?: string[];
	    homepage?: string;
	    repository?: string;
	    interface?: CodexPluginInterface;
	
	    static createFrom(source: any = {}) {
	        return new CodexPluginManifest(source);
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
	        this.interface = this.convertValues(source["interface"], CodexPluginInterface);
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
	export class CodexPluginDetail {
	    id: string;
	    name: string;
	    marketplace: string;
	    version?: string;
	    enabled: boolean;
	    installPath?: string;
	    manifestPath?: string;
	    installedAt?: string;
	    lastUpdated?: string;
	    source?: string;
	    warning?: string;
	    duplicateOf?: string;
	    manifest: CodexPluginManifest;
	    displayName?: string;
	    shortDescription?: string;
	    longDescription?: string;
	    skills: SkillInfo[];
	    agents: AgentInfo[];
	    commands: CommandInfo[];
	    hooks: HookInfo[];
	    hasMcp: boolean;
	    mcpServers?: Record<string, any>;
	    pluginType: string;
	
	    static createFrom(source: any = {}) {
	        return new CodexPluginDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.marketplace = source["marketplace"];
	        this.version = source["version"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.manifestPath = source["manifestPath"];
	        this.installedAt = source["installedAt"];
	        this.lastUpdated = source["lastUpdated"];
	        this.source = source["source"];
	        this.warning = source["warning"];
	        this.duplicateOf = source["duplicateOf"];
	        this.manifest = this.convertValues(source["manifest"], CodexPluginManifest);
	        this.displayName = source["displayName"];
	        this.shortDescription = source["shortDescription"];
	        this.longDescription = source["longDescription"];
	        this.skills = this.convertValues(source["skills"], SkillInfo);
	        this.agents = this.convertValues(source["agents"], AgentInfo);
	        this.commands = this.convertValues(source["commands"], CommandInfo);
	        this.hooks = this.convertValues(source["hooks"], HookInfo);
	        this.hasMcp = source["hasMcp"];
	        this.mcpServers = source["mcpServers"];
	        this.pluginType = source["pluginType"];
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
	
	
	export class CodexPluginsData {
	    marketplaces: CodexMarketplace[];
	    installed: CodexPlugin[];
	    available: CodexAvailablePlugin[];
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new CodexPluginsData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.marketplaces = this.convertValues(source["marketplaces"], CodexMarketplace);
	        this.installed = this.convertValues(source["installed"], CodexPlugin);
	        this.available = this.convertValues(source["available"], CodexAvailablePlugin);
	        this.warnings = source["warnings"];
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
	
	export class PluginSelector {
	    pluginId: string;
	    id?: string;
	    name?: string;
	    marketplace?: string;
	
	    static createFrom(source: any = {}) {
	        return new PluginSelector(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.id = source["id"];
	        this.name = source["name"];
	        this.marketplace = source["marketplace"];
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
	export class AnthropicFormat {
	    enabled: boolean;
	    api_key?: string;
	    base_url?: string;
	    auth_key?: string;
	    headers?: Record<string, string>;
	    auth_header?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AnthropicFormat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.api_key = source["api_key"];
	        this.base_url = source["base_url"];
	        this.auth_key = source["auth_key"];
	        this.headers = source["headers"];
	        this.auth_header = source["auth_header"];
	    }
	}
	export class OpenCodePresetSource {
	    kind?: string;
	    legacy_provider?: string;
	    legacy_preset_key?: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenCodePresetSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.legacy_provider = source["legacy_provider"];
	        this.legacy_preset_key = source["legacy_preset_key"];
	    }
	}
	export class OpenCodeBinding {
	    local_provider: string;
	    format?: string;
	    inject?: string[];
	    env_fallback?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenCodeBinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.local_provider = source["local_provider"];
	        this.format = source["format"];
	        this.inject = source["inject"];
	        this.env_fallback = source["env_fallback"];
	    }
	}
	export class OpenCodePreset {
	    id: string;
	    name: string;
	    description?: string;
	    config: number[];
	    bindings?: Record<string, OpenCodeBinding>;
	    source?: OpenCodePresetSource;
	
	    static createFrom(source: any = {}) {
	        return new OpenCodePreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.config = source["config"];
	        this.bindings = this.convertValues(source["bindings"], OpenCodeBinding, true);
	        this.source = this.convertValues(source["source"], OpenCodePresetSource);
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
	export class TerminalPreset {
	    name: string;
	    provider: string;
	    model: string;
	    model_haiku?: string;
	    model_sonnet?: string;
	    model_opus?: string;
	    parameters: Parameters;
	    opencode_cfg?: number[];
	
	    static createFrom(source: any = {}) {
	        return new TerminalPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.model_haiku = source["model_haiku"];
	        this.model_sonnet = source["model_sonnet"];
	        this.model_opus = source["model_opus"];
	        this.parameters = this.convertValues(source["parameters"], Parameters);
	        this.opencode_cfg = source["opencode_cfg"];
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
	export class TerminalPresetsConfig {
	    anthropic?: Record<string, TerminalPreset>;
	    openai?: Record<string, TerminalPreset>;
	    claude_code?: Record<string, TerminalPreset>;
	    opencode?: Record<string, TerminalPreset>;
	    codex?: Record<string, TerminalPreset>;
	    pi?: Record<string, TerminalPreset>;
	    omp?: Record<string, TerminalPreset>;
	
	    static createFrom(source: any = {}) {
	        return new TerminalPresetsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.anthropic = this.convertValues(source["anthropic"], TerminalPreset, true);
	        this.openai = this.convertValues(source["openai"], TerminalPreset, true);
	        this.claude_code = this.convertValues(source["claude_code"], TerminalPreset, true);
	        this.opencode = this.convertValues(source["opencode"], TerminalPreset, true);
	        this.codex = this.convertValues(source["codex"], TerminalPreset, true);
	        this.pi = this.convertValues(source["pi"], TerminalPreset, true);
	        this.omp = this.convertValues(source["omp"], TerminalPreset, true);
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
	    reasoning_effort?: string;
	    pi_compat?: Record<string, any>;
	
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
	        this.reasoning_effort = source["reasoning_effort"];
	        this.pi_compat = source["pi_compat"];
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
	    model_haiku?: string;
	    model_sonnet?: string;
	    model_opus?: string;
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
	        this.model_haiku = source["model_haiku"];
	        this.model_sonnet = source["model_sonnet"];
	        this.model_opus = source["model_opus"];
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
	export class OpenAIFormat {
	    enabled: boolean;
	    api_key?: string;
	    base_url?: string;
	    organization?: string;
	    auth_key?: string;
	    headers?: Record<string, string>;
	    auth_header?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenAIFormat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.api_key = source["api_key"];
	        this.base_url = source["base_url"];
	        this.organization = source["organization"];
	        this.auth_key = source["auth_key"];
	        this.headers = source["headers"];
	        this.auth_header = source["auth_header"];
	    }
	}
	export class Provider {
	    anthropic?: AnthropicFormat;
	    openai?: OpenAIFormat;
	    default_model: string;
	    url_history?: string[];
	    type?: string;
	    base_url?: string;
	    auth_key?: string;
	    presets?: Record<string, Preset>;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.anthropic = this.convertValues(source["anthropic"], AnthropicFormat);
	        this.openai = this.convertValues(source["openai"], OpenAIFormat);
	        this.default_model = source["default_model"];
	        this.url_history = source["url_history"];
	        this.type = source["type"];
	        this.base_url = source["base_url"];
	        this.auth_key = source["auth_key"];
	        this.presets = this.convertValues(source["presets"], Preset, true);
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
	    terminal_presets?: TerminalPresetsConfig;
	    opencode_presets?: Record<string, OpenCodePreset>;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new AppConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = this.convertValues(source["models"], Provider, true);
	        this.agent_teams = this.convertValues(source["agent_teams"], AgentTeamsConfig);
	        this.terminal_presets = this.convertValues(source["terminal_presets"], TerminalPresetsConfig);
	        this.opencode_presets = this.convertValues(source["opencode_presets"], OpenCodePreset, true);
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
	
	export class MergedTerminalPreset {
	    key: string;
	    label: string;
	    provider: string;
	    model: string;
	    model_haiku?: string;
	    model_sonnet?: string;
	    model_opus?: string;
	    parameters: Parameters;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new MergedTerminalPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.model_haiku = source["model_haiku"];
	        this.model_sonnet = source["model_sonnet"];
	        this.model_opus = source["model_opus"];
	        this.parameters = this.convertValues(source["parameters"], Parameters);
	        this.source = source["source"];
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

export namespace envcheck {
	
	export class ResolutionAction {
	    type: string;
	    description: string;
	    command?: string;
	    tool?: string;
	    packageName?: string;
	    requiresConfirm?: boolean;
	    isPrimary?: boolean;
	    method?: string;
	
	    static createFrom(source: any = {}) {
	        return new ResolutionAction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.description = source["description"];
	        this.command = source["command"];
	        this.tool = source["tool"];
	        this.packageName = source["packageName"];
	        this.requiresConfirm = source["requiresConfirm"];
	        this.isPrimary = source["isPrimary"];
	        this.method = source["method"];
	    }
	}
	export class CheckIssue {
	    severity: string;
	    code: string;
	    message: string;
	    detail?: string;
	    solutions?: ResolutionAction[];
	
	    static createFrom(source: any = {}) {
	        return new CheckIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.severity = source["severity"];
	        this.code = source["code"];
	        this.message = source["message"];
	        this.detail = source["detail"];
	        this.solutions = this.convertValues(source["solutions"], ResolutionAction);
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
	export class ClaudeConfigItem {
	    key: string;
	    filePath: string;
	    category: string;
	    required: boolean;
	    configured: boolean;
	    currentValue: string;
	    description: string;
	    defaultValue: string;
	
	    static createFrom(source: any = {}) {
	        return new ClaudeConfigItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.filePath = source["filePath"];
	        this.category = source["category"];
	        this.required = source["required"];
	        this.configured = source["configured"];
	        this.currentValue = source["currentValue"];
	        this.description = source["description"];
	        this.defaultValue = source["defaultValue"];
	    }
	}
	export class ClaudeConfigStatus {
	    configItems: ClaudeConfigItem[];
	    missingRequired: number;
	    allConfigured: boolean;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new ClaudeConfigStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configItems = this.convertValues(source["configItems"], ClaudeConfigItem);
	        this.missingRequired = source["missingRequired"];
	        this.allConfigured = source["allConfigured"];
	        this.warnings = source["warnings"];
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
	export class CheckStatus {
	    tool: string;
	    installed: boolean;
	    installMethod: string;
	    version: string;
	    hasUpdate: boolean;
	    latestVersion: string;
	    pathOk: boolean;
	    executablePath: string;
	    error: string;
	    // Go type: time
	    checkedAt: any;
	    systemPathOk: boolean;
	    pathState: string;
	    pathSource: string;
	    issues: CheckIssue[];
	    solutions: ResolutionAction[];
	    canInstall: boolean;
	    canInstallByMethod: Record<string, boolean>;
	    installBlockedReason: string;
	    config?: ClaudeConfigStatus;
	
	    static createFrom(source: any = {}) {
	        return new CheckStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool = source["tool"];
	        this.installed = source["installed"];
	        this.installMethod = source["installMethod"];
	        this.version = source["version"];
	        this.hasUpdate = source["hasUpdate"];
	        this.latestVersion = source["latestVersion"];
	        this.pathOk = source["pathOk"];
	        this.executablePath = source["executablePath"];
	        this.error = source["error"];
	        this.checkedAt = this.convertValues(source["checkedAt"], null);
	        this.systemPathOk = source["systemPathOk"];
	        this.pathState = source["pathState"];
	        this.pathSource = source["pathSource"];
	        this.issues = this.convertValues(source["issues"], CheckIssue);
	        this.solutions = this.convertValues(source["solutions"], ResolutionAction);
	        this.canInstall = source["canInstall"];
	        this.canInstallByMethod = source["canInstallByMethod"];
	        this.installBlockedReason = source["installBlockedReason"];
	        this.config = this.convertValues(source["config"], ClaudeConfigStatus);
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
	
	
	export class ConfigFixRequest {
	    key: string;
	    value: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigFixRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	        this.filePath = source["filePath"];
	    }
	}
	export class ConfigFixResult {
	    success: boolean;
	    message: string;
	    error?: string;
	    backupPath?: string;
	    changed: boolean;
	    previousValue?: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigFixResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.error = source["error"];
	        this.backupPath = source["backupPath"];
	        this.changed = source["changed"];
	        this.previousValue = source["previousValue"];
	    }
	}
	export class InstallResult {
	    success: boolean;
	    message: string;
	    tool: string;
	    version: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new InstallResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.tool = source["tool"];
	        this.version = source["version"];
	        this.error = source["error"];
	    }
	}
	export class OperationState {
	    id: string;
	    tool: string;
	    kind: string;
	    status: string;
	    step: string;
	    message: string;
	    progress: number;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    updatedAt: any;
	    // Go type: time
	    finishedAt?: any;
	    result?: InstallResult;
	    error: string;
	    cacheRefreshed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OperationState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.tool = source["tool"];
	        this.kind = source["kind"];
	        this.status = source["status"];
	        this.step = source["step"];
	        this.message = source["message"];
	        this.progress = source["progress"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.result = this.convertValues(source["result"], InstallResult);
	        this.error = source["error"];
	        this.cacheRefreshed = source["cacheRefreshed"];
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
	export class OverallStatus {
	    allOk: boolean;
	    items: Record<string, CheckStatus>;
	    issues: string[];
	    // Go type: time
	    checkedAt: any;
	    checking: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OverallStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allOk = source["allOk"];
	        this.items = this.convertValues(source["items"], CheckStatus, true);
	        this.issues = source["issues"];
	        this.checkedAt = this.convertValues(source["checkedAt"], null);
	        this.checking = source["checking"];
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
	export class EnvCheckSnapshot {
	    status?: OverallStatus;
	    operation?: OperationState;
	
	    static createFrom(source: any = {}) {
	        return new EnvCheckSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], OverallStatus);
	        this.operation = this.convertValues(source["operation"], OperationState);
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
	export class FixActionRequest {
	    action: string;
	    tool?: string;
	    extraPath?: string;
	    method?: string;
	    key?: string;
	    value?: string;
	    filePath?: string;
	
	    static createFrom(source: any = {}) {
	        return new FixActionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.tool = source["tool"];
	        this.extraPath = source["extraPath"];
	        this.method = source["method"];
	        this.key = source["key"];
	        this.value = source["value"];
	        this.filePath = source["filePath"];
	    }
	}
	export class FixActionResult {
	    success: boolean;
	    message: string;
	    error?: string;
	    profilePath?: string;
	    backupPath?: string;
	    addedPaths?: string[];
	    changed: boolean;
	    requiresRestart: boolean;
	    nextSteps?: string[];
	
	    static createFrom(source: any = {}) {
	        return new FixActionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.error = source["error"];
	        this.profilePath = source["profilePath"];
	        this.backupPath = source["backupPath"];
	        this.addedPaths = source["addedPaths"];
	        this.changed = source["changed"];
	        this.requiresRestart = source["requiresRestart"];
	        this.nextSteps = source["nextSteps"];
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
	export class GlobalSyncStatus {
	    supported: boolean;
	    platform: string;
	    enabled: boolean;
	    managedKeys: string[];
	    managedCount: number;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new GlobalSyncStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.platform = source["platform"];
	        this.enabled = source["enabled"];
	        this.managedKeys = source["managedKeys"];
	        this.managedCount = source["managedCount"];
	        this.message = source["message"];
	    }
	}

}

export namespace headroom {
	
	export class ClientPerfStat {
	    client: string;
	    requests: number;
	    avg_cache_hit_pct: number;
	    tokens_saved: number;
	    cache_read_tokens: number;
	    tokens_before: number;
	    savings_percent: number;
	
	    static createFrom(source: any = {}) {
	        return new ClientPerfStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.client = source["client"];
	        this.requests = source["requests"];
	        this.avg_cache_hit_pct = source["avg_cache_hit_pct"];
	        this.tokens_saved = source["tokens_saved"];
	        this.cache_read_tokens = source["cache_read_tokens"];
	        this.tokens_before = source["tokens_before"];
	        this.savings_percent = source["savings_percent"];
	    }
	}
	export class ClientSavings {
	    client: string;
	    tokens_saved: number;
	    tokens_before: number;
	    cost_usd: number;
	    calls: number;
	    savings_percent: number;
	
	    static createFrom(source: any = {}) {
	        return new ClientSavings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.client = source["client"];
	        this.tokens_saved = source["tokens_saved"];
	        this.tokens_before = source["tokens_before"];
	        this.cost_usd = source["cost_usd"];
	        this.calls = source["calls"];
	        this.savings_percent = source["savings_percent"];
	    }
	}
	export class HeadroomStatus {
	    running: boolean;
	    port: number;
	    backendUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new HeadroomStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.port = source["port"];
	        this.backendUrl = source["backendUrl"];
	    }
	}
	export class ModelSavings {
	    model: string;
	    tokens_saved: number;
	    tokens_before: number;
	    cost_usd: number;
	    calls: number;
	    savings_percent: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelSavings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.tokens_saved = source["tokens_saved"];
	        this.tokens_before = source["tokens_before"];
	        this.cost_usd = source["cost_usd"];
	        this.calls = source["calls"];
	        this.savings_percent = source["savings_percent"];
	    }
	}
	export class SavingsBucket {
	    tokens_saved: number;
	    tokens_before: number;
	    cost_usd: number;
	    calls: number;
	    savings_percent: number;
	
	    static createFrom(source: any = {}) {
	        return new SavingsBucket(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tokens_saved = source["tokens_saved"];
	        this.tokens_before = source["tokens_before"];
	        this.cost_usd = source["cost_usd"];
	        this.calls = source["calls"];
	        this.savings_percent = source["savings_percent"];
	    }
	}
	export class SavingsWindows {
	    today: SavingsBucket;
	    last_7_days: SavingsBucket;
	    all_time: SavingsBucket;
	
	    static createFrom(source: any = {}) {
	        return new SavingsWindows(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.today = this.convertValues(source["today"], SavingsBucket);
	        this.last_7_days = this.convertValues(source["last_7_days"], SavingsBucket);
	        this.all_time = this.convertValues(source["all_time"], SavingsBucket);
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
	export class SavingsReport {
	    schema_version: number;
	    path: string;
	    top_model: string;
	    lifetime: SavingsBucket;
	    windows: SavingsWindows;
	    by_model: ModelSavings[];
	    by_client: ClientSavings[];
	
	    static createFrom(source: any = {}) {
	        return new SavingsReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schema_version = source["schema_version"];
	        this.path = source["path"];
	        this.top_model = source["top_model"];
	        this.lifetime = this.convertValues(source["lifetime"], SavingsBucket);
	        this.windows = this.convertValues(source["windows"], SavingsWindows);
	        this.by_model = this.convertValues(source["by_model"], ModelSavings);
	        this.by_client = this.convertValues(source["by_client"], ClientSavings);
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

export namespace launchplan {
	
	export class CompensationDebt {
	    owner: string;
	    effect: number;
	    step: string;
	    disposition: string;
	    message?: string;
	    attempts: number;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new CompensationDebt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.owner = source["owner"];
	        this.effect = source["effect"];
	        this.step = source["step"];
	        this.disposition = source["disposition"];
	        this.message = source["message"];
	        this.attempts = source["attempts"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
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

export namespace main {
	
	export class ClearStoppedSessionFailure {
	    id: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new ClearStoppedSessionFailure(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.reason = source["reason"];
	    }
	}
	export class ClearStoppedSessionsResult {
	    cleared: number;
	    clearedIds: string[];
	    retainedIds: string[];
	    failed: ClearStoppedSessionFailure[];
	
	    static createFrom(source: any = {}) {
	        return new ClearStoppedSessionsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cleared = source["cleared"];
	        this.clearedIds = source["clearedIds"];
	        this.retainedIds = source["retainedIds"];
	        this.failed = this.convertValues(source["failed"], ClearStoppedSessionFailure);
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
	export class CodexGlobalHeadroomStatus {
	    enabled: boolean;
	    target: string;
	    port: number;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CodexGlobalHeadroomStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.target = source["target"];
	        this.port = source["port"];
	        this.running = source["running"];
	    }
	}
	export class OpenRemoteWebUIResult {
	    url: string;
	    port: number;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenRemoteWebUIResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.port = source["port"];
	        this.running = source["running"];
	    }
	}
	export class RemoteWebUIStatusResult {
	    openable: boolean;
	    reason: string;
	    url: string;
	    port: number;
	    running: boolean;
	    mobileWebRoot: string;
	    mobileWebRootConfigured: boolean;
	    mobileWebRootExists: boolean;
	    mobileWebEmbedded: boolean;
	    mobileWebAvailable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteWebUIStatusResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.openable = source["openable"];
	        this.reason = source["reason"];
	        this.url = source["url"];
	        this.port = source["port"];
	        this.running = source["running"];
	        this.mobileWebRoot = source["mobileWebRoot"];
	        this.mobileWebRootConfigured = source["mobileWebRootConfigured"];
	        this.mobileWebRootExists = source["mobileWebRootExists"];
	        this.mobileWebEmbedded = source["mobileWebEmbedded"];
	        this.mobileWebAvailable = source["mobileWebAvailable"];
	    }
	}

}

export namespace ompplugin {
	
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
	export class Plugin {
	    id: string;
	    name: string;
	    version?: string;
	    kind: string;
	    enabled: boolean;
	    enabledFeatures?: string[];
	    description?: string;
	    scope?: string;
	    installPath?: string;
	
	    static createFrom(source: any = {}) {
	        return new Plugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.kind = source["kind"];
	        this.enabled = source["enabled"];
	        this.enabledFeatures = source["enabledFeatures"];
	        this.description = source["description"];
	        this.scope = source["scope"];
	        this.installPath = source["installPath"];
	    }
	}
	export class PluginsData {
	    installed: Plugin[];
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PluginsData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = this.convertValues(source["installed"], Plugin);
	        this.warnings = source["warnings"];
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

export namespace opencodeplugin {
	
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
	export class Plugin {
	    id: string;
	    spec: string;
	    name: string;
	    version?: string;
	    description?: string;
	    author?: string;
	    repository?: string;
	    source: string;
	    scope: string;
	    enabled: boolean;
	    installPath?: string;
	    manifestPath?: string;
	    lastUpdated?: string;
	    targets: string[];
	
	    static createFrom(source: any = {}) {
	        return new Plugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.spec = source["spec"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.author = source["author"];
	        this.repository = source["repository"];
	        this.source = source["source"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.manifestPath = source["manifestPath"];
	        this.lastUpdated = source["lastUpdated"];
	        this.targets = source["targets"];
	    }
	}
	export class ResourceInfo {
	    name: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new ResourceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.filePath = source["filePath"];
	    }
	}
	export class PluginDetail {
	    id: string;
	    spec: string;
	    name: string;
	    version?: string;
	    description?: string;
	    author?: string;
	    repository?: string;
	    source: string;
	    scope: string;
	    enabled: boolean;
	    installPath?: string;
	    manifestPath?: string;
	    lastUpdated?: string;
	    targets: string[];
	    skills: ResourceInfo[];
	    agents: ResourceInfo[];
	    commands: ResourceInfo[];
	    hooks: ResourceInfo[];
	    hasMcp: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PluginDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.spec = source["spec"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.author = source["author"];
	        this.repository = source["repository"];
	        this.source = source["source"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.manifestPath = source["manifestPath"];
	        this.lastUpdated = source["lastUpdated"];
	        this.targets = source["targets"];
	        this.skills = this.convertValues(source["skills"], ResourceInfo);
	        this.agents = this.convertValues(source["agents"], ResourceInfo);
	        this.commands = this.convertValues(source["commands"], ResourceInfo);
	        this.hooks = this.convertValues(source["hooks"], ResourceInfo);
	        this.hasMcp = source["hasMcp"];
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
	export class PluginsData {
	    installed: Plugin[];
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PluginsData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = this.convertValues(source["installed"], Plugin);
	        this.warnings = source["warnings"];
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
	export class PathsConfig {
	    paths: PathEntry[];
	    defaultPath: string;
	
	    static createFrom(source: any = {}) {
	        return new PathsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = this.convertValues(source["paths"], PathEntry);
	        this.defaultPath = source["defaultPath"];
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
	export class PathsService {
	
	
	    static createFrom(source: any = {}) {
	        return new PathsService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace piplugin {
	
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
	export class Package {
	    id: string;
	    source: string;
	    sourceType: string;
	    name: string;
	    version?: string;
	    description?: string;
	    author?: string;
	    repository?: string;
	    scope: string;
	    enabled: boolean;
	    installPath?: string;
	    manifestPath?: string;
	    lastUpdated?: string;
	    pinned?: boolean;
	    extensions?: string[];
	    skills?: string[];
	    prompts?: string[];
	    themes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Package(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.sourceType = source["sourceType"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.author = source["author"];
	        this.repository = source["repository"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.manifestPath = source["manifestPath"];
	        this.lastUpdated = source["lastUpdated"];
	        this.pinned = source["pinned"];
	        this.extensions = source["extensions"];
	        this.skills = source["skills"];
	        this.prompts = source["prompts"];
	        this.themes = source["themes"];
	    }
	}
	export class ResourceInfo {
	    name: string;
	    filePath: string;
	    type: string;
	
	    static createFrom(source: any = {}) {
	        return new ResourceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.filePath = source["filePath"];
	        this.type = source["type"];
	    }
	}
	export class PackageDetail {
	    id: string;
	    source: string;
	    sourceType: string;
	    name: string;
	    version?: string;
	    description?: string;
	    author?: string;
	    repository?: string;
	    scope: string;
	    enabled: boolean;
	    installPath?: string;
	    manifestPath?: string;
	    lastUpdated?: string;
	    pinned?: boolean;
	    extensions?: string[];
	    skills?: string[];
	    prompts?: string[];
	    themes?: string[];
	    resources: ResourceInfo[];
	    manifestDeclared: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PackageDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.sourceType = source["sourceType"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.author = source["author"];
	        this.repository = source["repository"];
	        this.scope = source["scope"];
	        this.enabled = source["enabled"];
	        this.installPath = source["installPath"];
	        this.manifestPath = source["manifestPath"];
	        this.lastUpdated = source["lastUpdated"];
	        this.pinned = source["pinned"];
	        this.extensions = source["extensions"];
	        this.skills = source["skills"];
	        this.prompts = source["prompts"];
	        this.themes = source["themes"];
	        this.resources = this.convertValues(source["resources"], ResourceInfo);
	        this.manifestDeclared = source["manifestDeclared"];
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
	export class PackagesData {
	    installed: Package[];
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PackagesData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = this.convertValues(source["installed"], Package);
	        this.warnings = source["warnings"];
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

export namespace platform {
	
	export class ShellDescriptor {
	    key: string;
	    label: string;
	    resolvedPath: string;
	    available: boolean;
	    isDefault: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ShellDescriptor(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.resolvedPath = source["resolvedPath"];
	        this.available = source["available"];
	        this.isDefault = source["isDefault"];
	    }
	}
	export class PlatformCapabilities {
	    platformId: string;
	    os: string;
	    arch: string;
	    embeddedTerminalSupported: boolean;
	    standaloneTerminalSupported: boolean;
	    systemTraySupported: boolean;
	    fileOpenSupported: boolean;
	    updateCheckSupported: boolean;
	    updateInstallSupported: boolean;
	    autoStartSupported: boolean;
	    singleInstanceSupported: boolean;
	    windowActivationSupported: boolean;
	    hideOnCloseSupported: boolean;
	    backgroundResidentSupported: boolean;
	    closeAction: string;
	    secureSecretStoreKind: string;
	    pathDiagnosticsSupported: boolean;
	    supportedShells: ShellDescriptor[];
	    defaultShellKey: string;
	
	    static createFrom(source: any = {}) {
	        return new PlatformCapabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.platformId = source["platformId"];
	        this.os = source["os"];
	        this.arch = source["arch"];
	        this.embeddedTerminalSupported = source["embeddedTerminalSupported"];
	        this.standaloneTerminalSupported = source["standaloneTerminalSupported"];
	        this.systemTraySupported = source["systemTraySupported"];
	        this.fileOpenSupported = source["fileOpenSupported"];
	        this.updateCheckSupported = source["updateCheckSupported"];
	        this.updateInstallSupported = source["updateInstallSupported"];
	        this.autoStartSupported = source["autoStartSupported"];
	        this.singleInstanceSupported = source["singleInstanceSupported"];
	        this.windowActivationSupported = source["windowActivationSupported"];
	        this.hideOnCloseSupported = source["hideOnCloseSupported"];
	        this.backgroundResidentSupported = source["backgroundResidentSupported"];
	        this.closeAction = source["closeAction"];
	        this.secureSecretStoreKind = source["secureSecretStoreKind"];
	        this.pathDiagnosticsSupported = source["pathDiagnosticsSupported"];
	        this.supportedShells = this.convertValues(source["supportedShells"], ShellDescriptor);
	        this.defaultShellKey = source["defaultShellKey"];
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
	    description?: string;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
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
	    description?: string;
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
	        this.description = source["description"];
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
	    description?: string;
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
	        this.description = source["description"];
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

export namespace remote {
	
	export class DeviceInfo {
	    id: string;
	    name: string;
	    pairedAt: string;
	    lastSeenAt: string;
	    credentialExpiresAt: string;
	    revokedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new DeviceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.pairedAt = source["pairedAt"];
	        this.lastSeenAt = source["lastSeenAt"];
	        this.credentialExpiresAt = source["credentialExpiresAt"];
	        this.revokedAt = source["revokedAt"];
	    }
	}
	export class ExternalCleanupRecoveryItem {
	    sessionId: string;
	    kind: number;
	    reason: string;
	    state: string;
	    canConfirm: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExternalCleanupRecoveryItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.kind = source["kind"];
	        this.reason = source["reason"];
	        this.state = source["state"];
	        this.canConfirm = source["canConfirm"];
	    }
	}
	export class ExternalCleanupRecoveryResult {
	    sessionId: string;
	    cleared: boolean;
	    fenceReleased: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExternalCleanupRecoveryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.cleared = source["cleared"];
	        this.fenceReleased = source["fenceReleased"];
	    }
	}
	export class ExternalCleanupRecoveryStatus {
	    version: number;
	    blocked: boolean;
	    items: ExternalCleanupRecoveryItem[];
	
	    static createFrom(source: any = {}) {
	        return new ExternalCleanupRecoveryStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.blocked = source["blocked"];
	        this.items = this.convertValues(source["items"], ExternalCleanupRecoveryItem);
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
	export class PairingWindowInfo {
	    generation: number;
	    code: string;
	    expiresAt: string;
	    baseUrl?: string;
	    addressRequired: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PairingWindowInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generation = source["generation"];
	        this.code = source["code"];
	        this.expiresAt = source["expiresAt"];
	        this.baseUrl = source["baseUrl"];
	        this.addressRequired = source["addressRequired"];
	    }
	}
	export class PairingWindowStatus {
	    active: boolean;
	    generation?: number;
	    expiresAt?: string;
	    remainingAttempts?: number;
	
	    static createFrom(source: any = {}) {
	        return new PairingWindowStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active = source["active"];
	        this.generation = source["generation"];
	        this.expiresAt = source["expiresAt"];
	        this.remainingAttempts = source["remainingAttempts"];
	    }
	}
	export class RevokeDeviceResult {
	    device: DeviceInfo;
	    alreadyRevoked: boolean;
	    terminationRequestedConnections: number;
	    eventOutcome: string;
	    durabilityDegraded: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RevokeDeviceResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.device = this.convertValues(source["device"], DeviceInfo);
	        this.alreadyRevoked = source["alreadyRevoked"];
	        this.terminationRequestedConnections = source["terminationRequestedConnections"];
	        this.eventOutcome = source["eventOutcome"];
	        this.durabilityDegraded = source["durabilityDegraded"];
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
	export class SecurityEventRecord {
	    eventId: string;
	    kind: string;
	    occurredAt: string;
	    pairingGeneration?: number;
	    attempt?: number;
	    deviceId?: string;
	    carrier?: string;
	    routeClass?: string;
	    outcome?: string;
	
	    static createFrom(source: any = {}) {
	        return new SecurityEventRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.eventId = source["eventId"];
	        this.kind = source["kind"];
	        this.occurredAt = source["occurredAt"];
	        this.pairingGeneration = source["pairingGeneration"];
	        this.attempt = source["attempt"];
	        this.deviceId = source["deviceId"];
	        this.carrier = source["carrier"];
	        this.routeClass = source["routeClass"];
	        this.outcome = source["outcome"];
	    }
	}
	export class SecurityHealthIssue {
	    code: string;
	    active: boolean;
	    acknowledged: boolean;
	    firstObservedAt: string;
	    lastObservedAt: string;
	    occurrences: number;
	    droppedEventIds: number;
	    recentEventIds: string[];
	
	    static createFrom(source: any = {}) {
	        return new SecurityHealthIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.active = source["active"];
	        this.acknowledged = source["acknowledged"];
	        this.firstObservedAt = source["firstObservedAt"];
	        this.lastObservedAt = source["lastObservedAt"];
	        this.occurrences = source["occurrences"];
	        this.droppedEventIds = source["droppedEventIds"];
	        this.recentEventIds = source["recentEventIds"];
	    }
	}
	export class SecurityHealthSnapshot {
	    securityReady: boolean;
	    issues: SecurityHealthIssue[];
	
	    static createFrom(source: any = {}) {
	        return new SecurityHealthSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.securityReady = source["securityReady"];
	        this.issues = this.convertValues(source["issues"], SecurityHealthIssue);
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
	    title: string;
	    claudeSessionId: string;
	
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
	        this.title = source["title"];
	        this.claudeSessionId = source["claudeSessionId"];
	    }
	}

}

export namespace settings {
	
	export class SkinSettings {
	    enabled: boolean;
	    imageId: string;
	    dim: number;
	    blur: number;
	    opacity: number;
	
	    static createFrom(source: any = {}) {
	        return new SkinSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.imageId = source["imageId"];
	        this.dim = source["dim"];
	        this.blur = source["blur"];
	        this.opacity = source["opacity"];
	    }
	}
	export class RemoteLaunchDefaultV1 {
	    providerRef?: string;
	    presetRef?: string;
	    modelRef?: string;
	    shellRef?: string;
	    useHeadroom?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteLaunchDefaultV1(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerRef = source["providerRef"];
	        this.presetRef = source["presetRef"];
	        this.modelRef = source["modelRef"];
	        this.shellRef = source["shellRef"];
	        this.useHeadroom = source["useHeadroom"];
	    }
	}
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
	export class WorkDirEntry {
	    path: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkDirEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.label = source["label"];
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
	    openCodePresetKey: string;
	    mode: string;
	    shell: string;
	    claudeMode: string;
	    claudeShell: string;
	    openCodeMode: string;
	    openCodeShell: string;
	    codexMode: string;
	    codexShell: string;
	    piMode: string;
	    piShell: string;
	    ompMode: string;
	    ompShell: string;
	    amagiCodePreset: string;
	    amagiCodeMode: string;
	    amagiCodeShell: string;
	    useHeadroom: boolean;
	    codexGlobalHeadroom: boolean;
	    codexGlobalHeadroomTarget: string;
	    codexGlobalHeadroomPort: number;
	
	    static createFrom(source: any = {}) {
	        return new DashboardDefaults(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.preset = source["preset"];
	        this.openCodeProvider = source["openCodeProvider"];
	        this.openCodePreset = source["openCodePreset"];
	        this.openCodePresetKey = source["openCodePresetKey"];
	        this.mode = source["mode"];
	        this.shell = source["shell"];
	        this.claudeMode = source["claudeMode"];
	        this.claudeShell = source["claudeShell"];
	        this.openCodeMode = source["openCodeMode"];
	        this.openCodeShell = source["openCodeShell"];
	        this.codexMode = source["codexMode"];
	        this.codexShell = source["codexShell"];
	        this.piMode = source["piMode"];
	        this.piShell = source["piShell"];
	        this.ompMode = source["ompMode"];
	        this.ompShell = source["ompShell"];
	        this.amagiCodePreset = source["amagiCodePreset"];
	        this.amagiCodeMode = source["amagiCodeMode"];
	        this.amagiCodeShell = source["amagiCodeShell"];
	        this.useHeadroom = source["useHeadroom"];
	        this.codexGlobalHeadroom = source["codexGlobalHeadroom"];
	        this.codexGlobalHeadroomTarget = source["codexGlobalHeadroomTarget"];
	        this.codexGlobalHeadroomPort = source["codexGlobalHeadroomPort"];
	    }
	}
	export class AppSettings {
	    dashboard: DashboardDefaults;
	    shellPaths: ShellEntry[];
	    savedWorkDirs: WorkDirEntry[];
	    terminal: TerminalSettings;
	    remoteHost: string;
	    remotePort: number;
	    remoteEnabled: boolean;
	    remoteSecurityVersion?: number;
	    remoteLaunchDefaultsV1?: Record<string, RemoteLaunchDefaultV1>;
	    mobileWebRoot: string;
	    githubToken: string;
	    skin: SkinSettings;
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dashboard = this.convertValues(source["dashboard"], DashboardDefaults);
	        this.shellPaths = this.convertValues(source["shellPaths"], ShellEntry);
	        this.savedWorkDirs = this.convertValues(source["savedWorkDirs"], WorkDirEntry);
	        this.terminal = this.convertValues(source["terminal"], TerminalSettings);
	        this.remoteHost = source["remoteHost"];
	        this.remotePort = source["remotePort"];
	        this.remoteEnabled = source["remoteEnabled"];
	        this.remoteSecurityVersion = source["remoteSecurityVersion"];
	        this.remoteLaunchDefaultsV1 = this.convertValues(source["remoteLaunchDefaultsV1"], RemoteLaunchDefaultV1, true);
	        this.mobileWebRoot = source["mobileWebRoot"];
	        this.githubToken = source["githubToken"];
	        this.skin = this.convertValues(source["skin"], SkinSettings);
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
	export class CodexGlobalHeadroomState {
	    enabled: boolean;
	    target: string;
	    port: number;
	
	    static createFrom(source: any = {}) {
	        return new CodexGlobalHeadroomState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.target = source["target"];
	        this.port = source["port"];
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

export namespace skins {
	
	export class Skin {
	    id: string;
	    fileName: string;
	    url: string;
	    bytes: number;
	    width: number;
	    height: number;
	    importedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Skin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileName = source["fileName"];
	        this.url = source["url"];
	        this.bytes = source["bytes"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.importedAt = source["importedAt"];
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
	    releaseURL: string;
	    assetName: string;
	    assetURL: string;
	    assetSize: number;
	    updateAction: string;
	
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
	        this.releaseURL = source["releaseURL"];
	        this.assetName = source["assetName"];
	        this.assetURL = source["assetURL"];
	        this.assetSize = source["assetSize"];
	        this.updateAction = source["updateAction"];
	    }
	}

}

export namespace usage {
	
	export class DailyTrendPoint {
	    day: string;
	    totalCostUSD: number;
	    costByCurrency: Record<string, number>;
	    inputTokens: number;
	    outputTokens: number;
	    requests: number;
	
	    static createFrom(source: any = {}) {
	        return new DailyTrendPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.day = source["day"];
	        this.totalCostUSD = source["totalCostUSD"];
	        this.costByCurrency = source["costByCurrency"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.requests = source["requests"];
	    }
	}
	export class LogFilter {
	    startDate: string;
	    endDate: string;
	    appType: string;
	    source: string;
	    provider: string;
	    model: string;
	    page: number;
	    pageSize: number;
	
	    static createFrom(source: any = {}) {
	        return new LogFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	        this.appType = source["appType"];
	        this.source = source["source"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	    }
	}
	export class ModelDailyTrendPoint {
	    day: string;
	    normalizedModel: string;
	    displayName: string;
	    provider: string;
	    currencyCode: string;
	    inputTokens: number;
	    outputTokens: number;
	    cacheRead: number;
	    cacheCreation: number;
	    billableInput: number;
	    totalTokens: number;
	    cacheAdjustedTokens: number;
	    totalCost: number;
	    totalCostUSD: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelDailyTrendPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.day = source["day"];
	        this.normalizedModel = source["normalizedModel"];
	        this.displayName = source["displayName"];
	        this.provider = source["provider"];
	        this.currencyCode = source["currencyCode"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheCreation = source["cacheCreation"];
	        this.billableInput = source["billableInput"];
	        this.totalTokens = source["totalTokens"];
	        this.cacheAdjustedTokens = source["cacheAdjustedTokens"];
	        this.totalCost = source["totalCost"];
	        this.totalCostUSD = source["totalCostUSD"];
	    }
	}
	export class ModelPricing {
	    id: string;
	    modelPattern: string;
	    displayName: string;
	    provider: string;
	    currencyCode: string;
	    inputPerMillion: number;
	    outputPerMillion: number;
	    cacheReadPerMillion: number;
	    cacheCreationPerMillion: number;
	    isBuiltin: boolean;
	    notes?: string;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ModelPricing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.modelPattern = source["modelPattern"];
	        this.displayName = source["displayName"];
	        this.provider = source["provider"];
	        this.currencyCode = source["currencyCode"];
	        this.inputPerMillion = source["inputPerMillion"];
	        this.outputPerMillion = source["outputPerMillion"];
	        this.cacheReadPerMillion = source["cacheReadPerMillion"];
	        this.cacheCreationPerMillion = source["cacheCreationPerMillion"];
	        this.isBuiltin = source["isBuiltin"];
	        this.notes = source["notes"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
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
	export class ModelStat {
	    normalizedModel: string;
	    displayName: string;
	    provider: string;
	    currencyCode: string;
	    appType: string;
	    requests: number;
	    inputTokens: number;
	    outputTokens: number;
	    cacheRead: number;
	    cacheCreation: number;
	    billableInput: number;
	    totalTokens: number;
	    cacheHitRate: number;
	    cacheAdjustedTokens: number;
	    inputCost: number;
	    outputCost: number;
	    cacheReadCost: number;
	    cacheCreationCost: number;
	    totalCost: number;
	    cacheReadEstimatedCost: number;
	    cacheHitSavings: number;
	    hasPrice: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.normalizedModel = source["normalizedModel"];
	        this.displayName = source["displayName"];
	        this.provider = source["provider"];
	        this.currencyCode = source["currencyCode"];
	        this.appType = source["appType"];
	        this.requests = source["requests"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheCreation = source["cacheCreation"];
	        this.billableInput = source["billableInput"];
	        this.totalTokens = source["totalTokens"];
	        this.cacheHitRate = source["cacheHitRate"];
	        this.cacheAdjustedTokens = source["cacheAdjustedTokens"];
	        this.inputCost = source["inputCost"];
	        this.outputCost = source["outputCost"];
	        this.cacheReadCost = source["cacheReadCost"];
	        this.cacheCreationCost = source["cacheCreationCost"];
	        this.totalCost = source["totalCost"];
	        this.cacheReadEstimatedCost = source["cacheReadEstimatedCost"];
	        this.cacheHitSavings = source["cacheHitSavings"];
	        this.hasPrice = source["hasPrice"];
	    }
	}
	export class PricingService {
	
	
	    static createFrom(source: any = {}) {
	        return new PricingService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}
	export class ProviderStat {
	    provider: string;
	    requests: number;
	    totalCostUSD: number;
	    costByCurrency: Record<string, number>;
	    totalTokens: number;
	    modelCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ProviderStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.requests = source["requests"];
	        this.totalCostUSD = source["totalCostUSD"];
	        this.costByCurrency = source["costByCurrency"];
	        this.totalTokens = source["totalTokens"];
	        this.modelCount = source["modelCount"];
	    }
	}
	export class StatFilter {
	    startDate: string;
	    endDate: string;
	    appType: string;
	    source: string;
	    provider: string;
	
	    static createFrom(source: any = {}) {
	        return new StatFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	        this.appType = source["appType"];
	        this.source = source["source"];
	        this.provider = source["provider"];
	    }
	}
	export class SummaryDateRange {
	    start: string;
	    end: string;
	
	    static createFrom(source: any = {}) {
	        return new SummaryDateRange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.end = source["end"];
	    }
	}
	export class Summary {
	    totalRequests: number;
	    totalTokens: number;
	    totalInputTokens: number;
	    totalOutputTokens: number;
	    totalCacheRead: number;
	    totalCacheCreation: number;
	    totalBillableInput: number;
	    totalCostByCurrency: Record<string, number>;
	    totalCostUSD: number;
	    dateRange: SummaryDateRange;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalRequests = source["totalRequests"];
	        this.totalTokens = source["totalTokens"];
	        this.totalInputTokens = source["totalInputTokens"];
	        this.totalOutputTokens = source["totalOutputTokens"];
	        this.totalCacheRead = source["totalCacheRead"];
	        this.totalCacheCreation = source["totalCacheCreation"];
	        this.totalBillableInput = source["totalBillableInput"];
	        this.totalCostByCurrency = source["totalCostByCurrency"];
	        this.totalCostUSD = source["totalCostUSD"];
	        this.dateRange = this.convertValues(source["dateRange"], SummaryDateRange);
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
	
	export class SummaryFilter {
	    startDate: string;
	    endDate: string;
	    appType: string;
	    source: string;
	    provider: string;
	
	    static createFrom(source: any = {}) {
	        return new SummaryFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	        this.appType = source["appType"];
	        this.source = source["source"];
	        this.provider = source["provider"];
	    }
	}
	export class SyncResult {
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt: any;
	    duration: string;
	    recordsAdded: number;
	    processedCount: number;
	    filesScanned: number;
	    errors: string[];
	
	    static createFrom(source: any = {}) {
	        return new SyncResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.duration = source["duration"];
	        this.recordsAdded = source["recordsAdded"];
	        this.processedCount = source["processedCount"];
	        this.filesScanned = source["filesScanned"];
	        this.errors = source["errors"];
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
	export class SyncState {
	    sourceType: string;
	    sourceKey: string;
	    appType: string;
	    lastMTime: number;
	    lastLineOffset: number;
	    lastTimeUpdated: number;
	    lastProvider?: string;
	    lastModel?: string;
	    lastSessionId?: string;
	    lastProjectDir?: string;
	    // Go type: time
	    lastSyncedAt: any;
	    lastError?: string;
	    recordsAdded: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceType = source["sourceType"];
	        this.sourceKey = source["sourceKey"];
	        this.appType = source["appType"];
	        this.lastMTime = source["lastMTime"];
	        this.lastLineOffset = source["lastLineOffset"];
	        this.lastTimeUpdated = source["lastTimeUpdated"];
	        this.lastProvider = source["lastProvider"];
	        this.lastModel = source["lastModel"];
	        this.lastSessionId = source["lastSessionId"];
	        this.lastProjectDir = source["lastProjectDir"];
	        this.lastSyncedAt = this.convertValues(source["lastSyncedAt"], null);
	        this.lastError = source["lastError"];
	        this.recordsAdded = source["recordsAdded"];
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
	export class TrendFilter {
	    startDate: string;
	    endDate: string;
	    appType: string;
	    source: string;
	    provider: string;
	    granularity: string;
	    days: number;
	
	    static createFrom(source: any = {}) {
	        return new TrendFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	        this.appType = source["appType"];
	        this.source = source["source"];
	        this.provider = source["provider"];
	        this.granularity = source["granularity"];
	        this.days = source["days"];
	    }
	}
	export class UnknownModel {
	    normalizedModel: string;
	    sampleRaw: string;
	    requests: number;
	    lastSeen: string;
	
	    static createFrom(source: any = {}) {
	        return new UnknownModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.normalizedModel = source["normalizedModel"];
	        this.sampleRaw = source["sampleRaw"];
	        this.requests = source["requests"];
	        this.lastSeen = source["lastSeen"];
	    }
	}
	export class UsageEvent {
	    AppType: string;
	    Source: string;
	    Provider: string;
	    Model: string;
	    SessionID: string;
	    ProjectDir: string;
	    Preset: string;
	    InputTokens: number;
	    OutputTokens: number;
	    CacheReadInputTokens: number;
	    CacheCreationInputTokens: number;
	    // Go type: time
	    OccurredAt: any;
	    DedupKey: string;
	    CostProvided: boolean;
	    NativeCost: number;
	    CurrencyCode: string;
	
	    static createFrom(source: any = {}) {
	        return new UsageEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.AppType = source["AppType"];
	        this.Source = source["Source"];
	        this.Provider = source["Provider"];
	        this.Model = source["Model"];
	        this.SessionID = source["SessionID"];
	        this.ProjectDir = source["ProjectDir"];
	        this.Preset = source["Preset"];
	        this.InputTokens = source["InputTokens"];
	        this.OutputTokens = source["OutputTokens"];
	        this.CacheReadInputTokens = source["CacheReadInputTokens"];
	        this.CacheCreationInputTokens = source["CacheCreationInputTokens"];
	        this.OccurredAt = this.convertValues(source["OccurredAt"], null);
	        this.DedupKey = source["DedupKey"];
	        this.CostProvided = source["CostProvided"];
	        this.NativeCost = source["NativeCost"];
	        this.CurrencyCode = source["CurrencyCode"];
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
	export class UsageRecord {
	    dedupKey: string;
	    appType: string;
	    source: string;
	    provider: string;
	    model: string;
	    normalizedModel: string;
	    sessionId: string;
	    projectDir: string;
	    preset?: string;
	    inputTokens: number;
	    outputTokens: number;
	    cacheReadInputTokens: number;
	    cacheCreationInputTokens: number;
	    billableInputTokens: number;
	    inputCost: number;
	    outputCost: number;
	    cacheReadCost: number;
	    cacheCreationCost: number;
	    totalCost: number;
	    currencyCode: string;
	    costProvided: boolean;
	    // Go type: time
	    occurredAt: any;
	    // Go type: time
	    recordedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new UsageRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dedupKey = source["dedupKey"];
	        this.appType = source["appType"];
	        this.source = source["source"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.normalizedModel = source["normalizedModel"];
	        this.sessionId = source["sessionId"];
	        this.projectDir = source["projectDir"];
	        this.preset = source["preset"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheReadInputTokens = source["cacheReadInputTokens"];
	        this.cacheCreationInputTokens = source["cacheCreationInputTokens"];
	        this.billableInputTokens = source["billableInputTokens"];
	        this.inputCost = source["inputCost"];
	        this.outputCost = source["outputCost"];
	        this.cacheReadCost = source["cacheReadCost"];
	        this.cacheCreationCost = source["cacheCreationCost"];
	        this.totalCost = source["totalCost"];
	        this.currencyCode = source["currencyCode"];
	        this.costProvided = source["costProvided"];
	        this.occurredAt = this.convertValues(source["occurredAt"], null);
	        this.recordedAt = this.convertValues(source["recordedAt"], null);
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

export namespace webui {
	
	export class Status {
	    state: string;
	    url?: string;
	    port?: number;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.url = source["url"];
	        this.port = source["port"];
	    }
	}

}

