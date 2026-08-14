import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import CliLauncher from '../../../src/components/lobby/CliLauncher.vue';
import { KNOWN_CLI_TYPES, type LaunchSettings } from '../../../src/lib/contract';

const settings: LaunchSettings = {
  workdirs: [{ path: '/workspace', label: 'Workspace' }],
  shells: [{ path: '/bin/zsh', label: 'Zsh' }],
  clis: KNOWN_CLI_TYPES.map((cliType) => ({
    cliType,
    providers: [{ ref: 'provider-a', label: 'Provider A' }],
    presets: [{ ref: `${cliType}-preset`, label: `${cliType} preset`, providerRef: 'provider-a' }],
  })),
};

describe('CliLauncher', () => {
  it('renders and opens settings for all five supported terminals', async () => {
    const wrapper = mount(CliLauncher, {
      props: {
        availability: KNOWN_CLI_TYPES.map((cliType) => ({ cliType, available: true })),
        launching: null,
        settings,
      },
    });

    const cards = wrapper.findAll('.cli-card');
    expect(cards).toHaveLength(5);
    for (let index = 0; index < KNOWN_CLI_TYPES.length; index += 1) {
      await cards[index].trigger('click');
      expect(wrapper.find('.launch-settings').exists()).toBe(true);
    }
  });

  it('emits selected session settings in the create request', async () => {
    const wrapper = mount(CliLauncher, {
      props: {
        availability: KNOWN_CLI_TYPES.map((cliType) => ({ cliType, available: true })),
        launching: null,
        settings,
      },
    });

    await wrapper.findAll('.cli-card')[2].trigger('click');
    const selects = wrapper.findAll('select');
    await selects[0].setValue('provider-a');
    await selects[1].setValue('codex-preset');
    await selects[2].setValue('/bin/zsh');
    await wrapper.find('form').trigger('submit');

    expect(wrapper.emitted('launch')?.[0]?.[0]).toEqual({
      cliType: 'codex', workdir: '/workspace', providerRef: 'provider-a',
      presetRef: 'codex-preset', shellRef: '/bin/zsh',
    });
  });
});
