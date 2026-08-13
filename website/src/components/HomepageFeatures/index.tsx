import type {ReactNode} from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  icon: string;
  description: ReactNode;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Tree-sitter indexing',
    icon: '🌳',
    description: (
      <>
        Functions, classes, symbols, imports, and call graphs — parsed, not
        regexed. Nine languages supported out of the box.
      </>
    ),
  },
  {
    title: 'Hybrid search',
    icon: '🔍',
    description: (
      <>
        Keyword (FTS5), semantic (vector), structural, and change signals
        combined into a single ranked result.
      </>
    ),
  },
  {
    title: 'Budget-fitted context',
    icon: '🎯',
    description: (
      <>
        Ask for 4K tokens, get exactly 4K tokens of the most relevant code —
        no overflow, no padding.
      </>
    ),
  },
  {
    title: 'Local-first, always fresh',
    icon: '💻',
    description: (
      <>
        Runs entirely on your machine; your code never leaves it. A file
        watcher re-indexes on every save.
      </>
    ),
  },
];

function Feature({title, icon, description}: FeatureItem) {
  return (
    <div className={clsx('col col--3')}>
      <div className={styles.featureCard}>
        <span className={styles.featureIcon} aria-hidden="true">
          {icon}
        </span>
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
