// Shapes and form helpers for the Usenet and debrid settings page.
import { t } from '#lib/core/i18n/i18n.svelte.ts';

export type Kind = 'usenet' | 'debrid';
export type State = 'pending' | 'ok' | 'failed' | 'disabled';

// What the server shows in place of a saved secret.
export const MASKED = '••••••••';

export interface Entry {
	id: string;
	kind: Kind;
	provider?: 'realdebrid' | 'alldebrid';
	name: string;
	host?: string;
	port?: number;
	ssl?: boolean;
	username?: string;
	secret?: string;
	hasSecret: boolean;
	connections?: number;
	priority?: number;
	enabled: boolean;
	state: State;
	lastPush?: string;
	lastError?: string;
}

export interface Listing {
	entries: Entry[];
	unmanaged: { target: 'sabnzbd' | 'decypharr'; name: string; detail?: string }[];
	sabnzbd: { setUp: boolean; error?: string };
	decypharr: { setUp: boolean; error?: string };
}

export interface Form {
	id?: string;
	kind: Kind;
	provider: 'realdebrid' | 'alldebrid';
	name: string;
	host: string;
	port: number;
	ssl: boolean;
	username: string;
	secret: string;
	connections: number;
	priority: number;
	enabled: boolean;
}

// The default debrid name is not a provider's plain name, which a hand-made
// Decypharr account is likely to use already.
export function defaultDebridName(p: 'realdebrid' | 'alldebrid'): string {
	return `holyarrs-${p}`;
}

export function blankForm(kind: Kind): Form {
	return { kind, provider: 'realdebrid', name: kind === 'debrid' ? defaultDebridName('realdebrid') : '', host: '', port: 563, ssl: true, username: '', secret: '', connections: 8, priority: 0, enabled: true };
}

// An edit starts from the saved entry; the secret stays masked unless replaced.
export function formOf(e: Entry): Form {
	return {
		id: e.id, kind: e.kind, provider: e.provider ?? 'realdebrid', name: e.name, host: e.host ?? '', port: e.port ?? 563, ssl: !!e.ssl,
		username: e.username ?? '', secret: e.hasSecret ? MASKED : '', connections: e.connections ?? 8, priority: e.priority ?? 0, enabled: e.enabled
	};
}

// The body to send. An empty or masked secret on an edit keeps the saved one.
export function inputOf(f: Form): Record<string, unknown> {
	const base = { kind: f.kind, name: f.name.trim(), secret: f.secret === MASKED ? '' : f.secret, enabled: f.enabled };
	if (f.kind === 'debrid') return { ...base, provider: f.provider };
	return { ...base, host: f.host.trim(), port: Number(f.port), ssl: f.ssl, username: f.username.trim(), connections: Number(f.connections), priority: Number(f.priority) };
}

export function stateWord(s: State): string {
	return t(`settings.providers.state.${s}`);
}
