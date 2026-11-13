import { describe, expect, it, vi } from 'vitest';
import { openPlay, playPath, playShow, scanPath, type PlayAnswer } from './play.ts';

const a = (x: Partial<PlayAnswer>): PlayAnswer => ({ url: '', state: 'no_file', ...x });

describe('play', () => {
	it('builds the movie and episode routes', () => {
		expect(playPath(7)).toBe('/titles/7/play');
		expect(playPath(7, 42)).toBe('/titles/7/play?item=42');
		expect(scanPath(7, 42)).toBe('/titles/7/play/scan?item=42');
	});
	it('shows a link, a reason, or nothing', () => {
		expect(playShow(null)).toBe('none');
		expect(playShow(a({ state: 'ready', url: 'http://h/web/#/details?id=1' }))).toBe('play');
		expect(playShow(a({ state: 'ready' }))).toBe('none');
		expect(playShow(a({ state: 'not_added' }))).toBe('wait');
		expect(playShow(a({ state: 'no_answer' }))).toBe('wait');
		expect(playShow(a({ state: 'no_file' }))).toBe('none');
		expect(playShow(a({ state: 'no_server' }))).toBe('none');
	});
	it('opens only http links, in a new tab without an opener', () => {
		const open = vi.fn();
		openPlay('http://127.0.0.1:8096/web/#/details?id=x&serverId=y', open);
		expect(open).toHaveBeenCalledWith('http://127.0.0.1:8096/web/#/details?id=x&serverId=y', '_blank', 'noopener,noreferrer');
		open.mockClear();
		openPlay('javascript:alert(1)', open);
		expect(open).not.toHaveBeenCalled();
	});
});
