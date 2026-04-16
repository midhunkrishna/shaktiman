import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const GITHUB_REPO = 'https://github.com/midhunkrishna/shaktiman';
const EDIT_URL_BASE = `${GITHUB_REPO}/tree/master/website/`;

const config: Config = {
  title: 'Shaktiman',
  tagline: 'Local-first code context engine for coding agents',
  favicon: 'img/favicon.png',

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

  // `format: 'detect'` means .mdx files are parsed as MDX (admonitions work,
  // JSX enabled) and .md files are parsed as plain CommonMark (safer for
  // imported design docs with ASCII diagrams, `<1ms` tokens, etc.).
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

  plugins: [
    [
      '@docusaurus/plugin-client-redirects',
      {
        // Register `{from, to}` pairs here whenever a page moves or is
        // consolidated into another. Docusaurus emits a static HTML stub at
        // `from` that bounces the browser to `to`, keeping external bookmarks
        // and search-engine results working.
        redirects: [
          // C.1 — design/overview placeholder deleted; point old links at
          // the architecture page, which is the closest non-trivial target.
          {from: '/design/overview', to: '/design/architecture'},
        ],
      },
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
        src: 'img/logo.png',
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
