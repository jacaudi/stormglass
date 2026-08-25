import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { SettingsPanel } from './SettingsPanel';
import type { UserPreferences } from '../types/weather';

const prefs: UserPreferences = {
  temperatureUnit: 'F',
  windUnit: 'mph',
  pressureUnit: 'inHg',
  rainUnit: 'in',
  distanceUnit: 'mi',
  theme: 'liquid-glass',
  recordsWindowDays: 7,
};

describe('SettingsPanel', () => {
  it('does not render the dead Station ID / API Token inputs (token is server-side now)', () => {
    render(
      <SettingsPanel
        isOpen={true}
        prefs={prefs}
        onPrefsChange={vi.fn()}
        onClose={vi.fn()}
      />
    );

    expect(screen.queryByText(/station id/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/api token/i)).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/personal access token/i)).not.toBeInTheDocument();
  });

  it('still renders the real settings (units and theme)', () => {
    render(
      <SettingsPanel
        isOpen={true}
        prefs={prefs}
        onPrefsChange={vi.fn()}
        onClose={vi.fn()}
      />
    );

    expect(screen.getByText('Temperature')).toBeInTheDocument();
    expect(screen.getByText('°F')).toBeInTheDocument();
    expect(screen.getByText('Theme')).toBeInTheDocument();
  });

  it('renders each theme swatch with a non-empty inline background-image (§14)', () => {
    const { container } = render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    const swatches = container.querySelectorAll<HTMLElement>('.theme-swatch');
    expect(swatches.length).toBeGreaterThan(0);
    swatches.forEach((swatch) => {
      expect(swatch.style.backgroundImage).not.toBe('');
    });
  });
});

describe('SettingsPanel Records window selector (Task 7)', () => {
  it('renders a Records section with a Window row and the four window buttons', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    expect(screen.getByText('Records')).toBeInTheDocument();
    expect(screen.getByText('Window')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '7 days' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '30 days' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '180 days' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '365 days' })).toBeInTheDocument();
  });

  it('marks the button matching prefs.recordsWindowDays as active', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    expect(screen.getByRole('button', { name: '7 days' })).toHaveClass('active');
    expect(screen.getByRole('button', { name: '30 days' })).not.toHaveClass('active');
  });

  it('calls onPrefsChange with the selected window when a button is clicked', () => {
    const onPrefsChange = vi.fn();
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={onPrefsChange} onClose={vi.fn()} />
    );

    fireEvent.click(screen.getByRole('button', { name: '30 days' }));

    expect(onPrefsChange).toHaveBeenCalledWith({ recordsWindowDays: 30 });
  });
});

describe('SettingsPanel dialog semantics (P2.14)', () => {
  it('exposes role="dialog", aria-modal="true", and an accessible name from the h2', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Settings');
  });

  it('calls onClose when Escape is pressed', () => {
    const onClose = vi.fn();
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={onClose} />
    );

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('moves focus into the dialog on open', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    const dialog = screen.getByRole('dialog');
    expect(dialog.contains(document.activeElement)).toBe(true);
  });

  it('restores focus to the previously focused element when the dialog closes', () => {
    const trigger = document.createElement('button');
    document.body.appendChild(trigger);
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    const { rerender } = render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );
    expect(document.activeElement).not.toBe(trigger);

    rerender(
      <SettingsPanel isOpen={false} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );
    expect(document.activeElement).toBe(trigger);

    trigger.remove();
  });

  it('traps Tab: pressing Tab on the last focusable element wraps focus to the first', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    const dialog = screen.getByRole('dialog');
    const focusable = dialog.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    const first = focusable[0];
    const last = focusable[focusable.length - 1];

    last.focus();
    fireEvent.keyDown(dialog, { key: 'Tab' });

    expect(document.activeElement).toBe(first);
  });

  it('traps Shift+Tab: pressing Shift+Tab on the first focusable element wraps focus to the last', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    const dialog = screen.getByRole('dialog');
    const focusable = dialog.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    const first = focusable[0];
    const last = focusable[focusable.length - 1];

    first.focus();
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });

    expect(document.activeElement).toBe(last);
  });

  it('traps Shift+Tab when focus is still on the dialog container itself (just-opened state)', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );

    const dialog = screen.getByRole('dialog');
    const focusable = dialog.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    const last = focusable[focusable.length - 1];

    // On open, focus lands on the dialog container itself (not first/last).
    expect(document.activeElement).toBe(dialog);

    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });

    expect(document.activeElement).toBe(last);
  });
});

// Lightning is the only distance the dashboard shows today, and it arrives in
// km from a sensor whose range is quoted in km -- so the unit has to be a
// reader preference rather than a constant.
describe('SettingsPanel distance unit', () => {
  it('offers km and mi, with the current preference marked active', () => {
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={vi.fn()} onClose={vi.fn()} />
    );
    expect(screen.getByText('Distance')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'mi' })).toHaveClass('active');
    expect(screen.getByRole('button', { name: 'km' })).not.toHaveClass('active');
  });

  it('reports the change rather than mutating prefs itself', () => {
    const onPrefsChange = vi.fn();
    render(
      <SettingsPanel isOpen={true} prefs={prefs} onPrefsChange={onPrefsChange} onClose={vi.fn()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'km' }));
    expect(onPrefsChange).toHaveBeenCalledWith({ distanceUnit: 'km' });
  });
});
