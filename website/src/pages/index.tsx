import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import CodeBlock from '@theme/CodeBlock';
import Heading from '@theme/Heading';
import HomepageFeatures from '@site/src/components/HomepageFeatures';

import styles from './index.module.css';

const QUICKSTART = `git clone https://github.com/midhunkrishna/shaktiman.git
cd shaktiman
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktiman ./cmd/shaktiman
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktimand ./cmd/shaktimand
shaktiman init . && shaktiman index .`;

const HERO_STATS = [
  {value: '1.6s', label: 'to index a 233-file repo'},
  {value: '9', label: 'languages out of the box'},
  {value: '100%', label: 'local — your code never leaves your machine'},
];

function Hero() {
  return (
    <header className={styles.hero}>
      <div className="container">
        <div className={styles.heroRow}>
          <div className={styles.heroInner}>
            <Heading as="h1" className={styles.heroTitle}>
              Exactly the right code context for your coding agent
            </Heading>
            <p className={styles.heroBenefits}>
              Fewer tokens. Better recall. Zero cloud.
            </p>
            <p className={styles.heroSubtitle}>
              Coding agents burn their context window rediscovering your
              codebase — grep after grep, whole files read for one relevant
              function. Shaktiman indexes it once with tree-sitter and serves
              search, symbols, dependencies, and budget-fitted context over
              MCP, entirely on your machine.
            </p>
            <div className={styles.heroButtons}>
              <Link
                className={styles.ctaPrimary}
                to="/getting-started/quickstart">
                Get Started
              </Link>
              <Link
                className={styles.ctaGhost}
                href="https://github.com/midhunkrishna/shaktiman">
                View on GitHub
              </Link>
            </div>
          </div>
          <aside className={styles.heroStats}>
            {HERO_STATS.map((stat) => (
              <div key={stat.value} className={styles.heroStat}>
                <span className={styles.heroStatValue}>{stat.value}</span>
                <span className={styles.heroStatLabel}>{stat.label}</span>
              </div>
            ))}
          </aside>
        </div>
      </div>
    </header>
  );
}

/* Recorded with vhs — see demo/demo.tape in the repo to re-record. */
function Demo() {
  return (
    <section className={styles.demo}>
      <div className="container">
        <img
          src="/img/demo.gif"
          alt="Terminal demo: indexing a repository, hybrid search, symbol dependencies, and budget-fitted context assembly"
          className={styles.demoGif}
        />
      </div>
    </section>
  );
}

function WorksWith() {
  return (
    <section className={styles.worksWith}>
      <div className="container">
        <p className={styles.worksWithLabel}>Works with</p>
        <div className={styles.worksWithRow}>
          <Link to="/integrations/claude-code">Claude Code</Link>
          <Link to="/integrations/cursor">Cursor</Link>
          <Link to="/integrations/zed">Zed</Link>
          <Link to="/integrations/generic-mcp-client">Any MCP client</Link>
        </div>
      </div>
    </section>
  );
}

function Quickstart() {
  return (
    <section className={styles.quickstart}>
      <div className="container">
        <Heading as="h2" className={styles.sectionTitle}>
          Up and running in a minute
        </Heading>
        <div className={styles.quickstartBlock}>
          <CodeBlock language="bash">{QUICKSTART}</CodeBlock>
        </div>
        <p className={styles.quickstartNote}>
          Then wire it into Claude Code via <code>.mcp.json</code> — see{' '}
          <Link to="/getting-started/claude-code-setup">Claude Code setup</Link>{' '}
          for the full walkthrough.
        </p>
      </div>
    </section>
  );
}

function ClosingCta() {
  return (
    <section className={styles.closing}>
      <div className="container">
        <Heading as="h2" className={styles.sectionTitle}>
          Give your agent a better map
        </Heading>
        <div className={styles.closingButton}>
          <Link className={styles.ctaPrimary} to="/getting-started/quickstart">
            Get Started
          </Link>
        </div>
        <p className={styles.closingNote}>
          MIT-licensed, open source, and local-first.
        </p>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout description={siteConfig.tagline}>
      <div className={styles.page}>
        <Hero />
        <main>
          <Demo />
          <WorksWith />
          <HomepageFeatures />
          <Quickstart />
          <ClosingCta />
        </main>
      </div>
    </Layout>
  );
}
