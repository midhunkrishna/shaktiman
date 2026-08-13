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

function Hero() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={styles.hero}>
      <div className="container">
        <Heading as="h1" className={styles.heroTitle}>
          {siteConfig.title}
        </Heading>
        <p className={styles.heroTagline}>{siteConfig.tagline}</p>
        <p className={styles.heroSubtitle}>
          Coding agents burn their context window rediscovering your codebase —
          grep after grep, whole files read for one relevant function. Shaktiman
          indexes it once with tree-sitter and serves search, symbols,
          dependencies, and budget-fitted context over MCP, entirely on your
          machine.
        </p>
        <div className={styles.heroButtons}>
          <Link
            className="button button--primary button--lg"
            to="/getting-started/quickstart">
            Get Started
          </Link>
          <Link
            className="button button--secondary button--lg"
            href="https://github.com/midhunkrishna/shaktiman">
            GitHub
          </Link>
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
        <p>
          Works with <Link to="/integrations/claude-code">Claude Code</Link>,{' '}
          <Link to="/integrations/cursor">Cursor</Link>,{' '}
          <Link to="/integrations/zed">Zed</Link>, and{' '}
          <Link to="/integrations/generic-mcp-client">any MCP client</Link>.
        </p>
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

export default function Home(): ReactNode {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout description={siteConfig.tagline}>
      <Hero />
      <main>
        <Demo />
        <WorksWith />
        <HomepageFeatures />
        <Quickstart />
      </main>
    </Layout>
  );
}
