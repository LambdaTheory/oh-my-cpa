FROM mcr.microsoft.com/playwright:v1.62.1-noble

ENV PROFILE_DIR=/data/profile \
    HEADLESS=true

WORKDIR /app

RUN npm install --global pnpm@11.19.0 \
    && mkdir -p /data/profile \
    && chown -R pwuser:pwuser /data /app

COPY --chown=pwuser:pwuser package.json pnpm-lock.yaml tsconfig.json ./
RUN pnpm --ignore-workspace install --frozen-lockfile

COPY --chown=pwuser:pwuser src ./src

USER pwuser
ENTRYPOINT ["pnpm"]
CMD ["start"]
