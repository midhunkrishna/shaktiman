import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const GITHUB_REPO = 'https://github.com/midhunkrishna/shaktiman';
const EDIT_URL_BASE = `${GITHUB_REPO}/tree/master/website/`;

const config: Config = {
  title: 'Shaktiman',
  tagline: 'Local-first code context engine for coding agents',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  // Placeholder — swap for the real custom domain once DNS is set up.
  url: 'https://shaktiman.pages.dev',
  baseUrl: '/',

  organizationName: 'midhunkrishna',
  projectName: 'shaktiman',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  // Treat .md as CommonMark and .mdx as MDX. Without this, imported design docs
  // (which contain angle-bracket content like "<1ms" and inline HTML-ish tokens)
  // fail MDX parsing.
  markdown: {
    format: 'detect',
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: '/',
          editUrl: EDIT_URL_BASE,
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Shaktiman',
      logo: {
        alt: 'Shaktiman',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'mainSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          href: GITHUB_REPO,
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'light',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Getting Started', to: '/getting-started/installation'},
            {label: 'MCP Tools', to: '/reference/mcp-tools/overview'},
            {label: 'Configuration', to: '/configuration/config-file'},
            {label: 'Troubleshooting', to: '/troubleshooting/overview'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'GitHub', href: GITHUB_REPO},
            {label: 'Changelog', to: '/changelog'},
            {label: 'License (MIT)', href: `${GITHUB_REPO}/blob/master/LICENSE`},
          ],
        },
      ],
      copyright: `MIT licensed. © ${new Date().getFullYear()} Shaktiman contributors.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'go', 'toml', 'json', 'yaml', 'sql'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
